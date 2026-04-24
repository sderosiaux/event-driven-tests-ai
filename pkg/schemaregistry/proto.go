package schemaregistry

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/bufbuild/protocompile"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

// protoHandler implements formatHandler for Confluent-style Protobuf schemas.
//
// Confluent SR stores the .proto source text verbatim. We use bufbuild's
// protocompile to link it into a runtime FileDescriptor and dynamicpb for
// schema-less message round-trips.
//
// Wire format (Confluent Protobuf):
//
//	[magic:1][schema_id:4][msg_index_array][payload]
//
// where msg_index_array is either a single 0x00 byte (shorthand for "message
// index [0]", i.e. the first top-level message in the file) or a varint length
// N followed by N varint indices walking the nested-message tree. See
// https://docs.confluent.io/platform/current/schema-registry/fundamentals/serdes-develop/index.html#wire-format.
//
// This handler supports:
//   - single-file .proto schemas (no SR references/imports)
//   - the common "first message in file" case for both encode and decode
//   - arbitrary message-index paths on decode
//
// Imports / SR references are out of scope for M6-T4 and surface as a clear
// error so callers know to pre-resolve references themselves.
type protoHandler struct {
	file protoreflect.FileDescriptor
	// top is the message the schema's subject binds to. Confluent convention
	// is "first top-level message in the file"; we honour that here.
	top protoreflect.MessageDescriptor
}

func newProtoHandler(source string) (formatHandler, error) {
	if strings.Contains(source, "\nimport ") || strings.HasPrefix(strings.TrimSpace(source), "import ") {
		return nil, fmt.Errorf("schemaregistry: PROTOBUF schemas with imports (SR references) are not supported yet — inline the dependency or stage the proto via a pre-resolved descriptor")
	}
	const path = "schema.proto"
	compiler := protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{
			Accessor: protocompile.SourceAccessorFromMap(map[string]string{
				path: source,
			}),
		}),
	}
	files, err := compiler.Compile(context.Background(), path)
	if err != nil {
		return nil, fmt.Errorf("schemaregistry: compile proto schema: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("schemaregistry: proto compile produced no descriptors")
	}
	file := files[0]
	msgs := file.Messages()
	if msgs.Len() == 0 {
		return nil, fmt.Errorf("schemaregistry: proto schema has no messages")
	}
	return &protoHandler{file: file, top: msgs.Get(0)}, nil
}

func (h *protoHandler) encode(value any) ([]byte, error) {
	m := dynamicpb.NewMessage(h.top)
	if err := applyValue(m, value); err != nil {
		return nil, fmt.Errorf("schemaregistry: proto encode: %w", err)
	}
	body, err := proto.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("schemaregistry: proto marshal: %w", err)
	}
	// "[0]" message-index shorthand = single zero byte. Always safe when the
	// schema's target message is the first top-level definition, which is the
	// convention we enforce in newProtoHandler.
	return append([]byte{0x00}, body...), nil
}

func (h *protoHandler) decode(body []byte) (any, error) {
	msg, tail, err := resolveMessage(h.file, h.top, body)
	if err != nil {
		return nil, err
	}
	m := dynamicpb.NewMessage(msg)
	if err := proto.Unmarshal(tail, m); err != nil {
		return nil, fmt.Errorf("schemaregistry: proto unmarshal: %w", err)
	}
	return protoToMap(m), nil
}

// resolveMessage parses the Confluent message-index prefix and descends into
// nested messages if the schema author encoded a non-default target.
func resolveMessage(file protoreflect.FileDescriptor, def protoreflect.MessageDescriptor, data []byte) (protoreflect.MessageDescriptor, []byte, error) {
	if len(data) == 0 {
		return nil, nil, fmt.Errorf("schemaregistry: empty proto payload after schema id")
	}
	// Shorthand: single 0x00 byte means "[0]" (first top-level message).
	if data[0] == 0x00 {
		return file.Messages().Get(0), data[1:], nil
	}
	n, read := binary.Varint(data)
	if read <= 0 {
		return nil, nil, fmt.Errorf("schemaregistry: malformed proto message-index length")
	}
	data = data[read:]
	indices := make([]int, 0, n)
	for i := int64(0); i < n; i++ {
		v, r := binary.Varint(data)
		if r <= 0 {
			return nil, nil, fmt.Errorf("schemaregistry: malformed proto message-index entry %d", i)
		}
		indices = append(indices, int(v))
		data = data[r:]
	}
	msg, err := descendIndices(file, indices)
	if err != nil {
		return nil, nil, err
	}
	_ = def // def is the preferred target but we honour the indices on decode
	return msg, data, nil
}

func descendIndices(file protoreflect.FileDescriptor, indices []int) (protoreflect.MessageDescriptor, error) {
	if len(indices) == 0 {
		return nil, fmt.Errorf("schemaregistry: empty proto message-index path")
	}
	msgs := file.Messages()
	if indices[0] < 0 || indices[0] >= msgs.Len() {
		return nil, fmt.Errorf("schemaregistry: top-level message index %d out of range (len=%d)", indices[0], msgs.Len())
	}
	cur := msgs.Get(indices[0])
	for i, idx := range indices[1:] {
		nested := cur.Messages()
		if idx < 0 || idx >= nested.Len() {
			return nil, fmt.Errorf("schemaregistry: nested message index %d at depth %d out of range (len=%d)", idx, i+1, nested.Len())
		}
		cur = nested.Get(idx)
	}
	return cur, nil
}

// protoToMap walks a dynamic message back into JSON-friendly Go values so
// orchestrator matchers and CEL checks can reason about payloads.
func protoToMap(m protoreflect.Message) map[string]any {
	out := make(map[string]any, m.Descriptor().Fields().Len())
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		out[string(fd.Name())] = valueToAny(fd, v)
		return true
	})
	return out
}

func valueToAny(fd protoreflect.FieldDescriptor, v protoreflect.Value) any {
	switch {
	case fd.IsList():
		list := v.List()
		out := make([]any, list.Len())
		for i := 0; i < list.Len(); i++ {
			out[i] = scalarToAny(fd, list.Get(i))
		}
		return out
	case fd.IsMap():
		mp := v.Map()
		out := make(map[string]any, mp.Len())
		mp.Range(func(mk protoreflect.MapKey, mv protoreflect.Value) bool {
			out[mk.String()] = scalarToAny(fd.MapValue(), mv)
			return true
		})
		return out
	}
	return scalarToAny(fd, v)
}

func scalarToAny(fd protoreflect.FieldDescriptor, v protoreflect.Value) any {
	switch fd.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return protoToMap(v.Message())
	case protoreflect.EnumKind:
		return string(fd.Enum().Values().ByNumber(v.Enum()).Name())
	case protoreflect.BytesKind:
		return append([]byte(nil), v.Bytes()...)
	}
	return v.Interface()
}

// applyValue copies a JSON-shaped input (typically map[string]any from the
// orchestrator's data generator) into a dynamic message. Fields not present in
// the schema are silently dropped; type mismatches produce errors.
func applyValue(m *dynamicpb.Message, value any) error {
	src, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("proto encode expects map[string]any, got %T", value)
	}
	fields := m.Descriptor().Fields()
	for k, raw := range src {
		fd := fields.ByName(protoreflect.Name(k))
		if fd == nil {
			// Unknown field: let it roll through so JSON-shaped payloads with
			// extra keys don't fail the whole encode. Schema evolution happy path.
			continue
		}
		if err := assignField(m, fd, raw); err != nil {
			return fmt.Errorf("field %q: %w", k, err)
		}
	}
	return nil
}

// assignField handles the list/map/scalar dispatch for encode. Lists and maps
// need the owner message (m) to construct a mutable container, which is why
// this helper is split from scalarToProto.
func assignField(m *dynamicpb.Message, fd protoreflect.FieldDescriptor, raw any) error {
	switch {
	case fd.IsList():
		arr, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("expected []any for repeated field, got %T", raw)
		}
		list := m.Mutable(fd).List()
		for _, item := range arr {
			v, err := scalarToProto(fd, item)
			if err != nil {
				return err
			}
			list.Append(v)
		}
		return nil
	case fd.IsMap():
		return fmt.Errorf("proto encode for map fields not implemented yet")
	}
	v, err := scalarToProto(fd, raw)
	if err != nil {
		return err
	}
	m.Set(fd, v)
	return nil
}

func scalarToProto(fd protoreflect.FieldDescriptor, raw any) (protoreflect.Value, error) {
	switch fd.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		nested, ok := raw.(map[string]any)
		if !ok {
			return protoreflect.Value{}, fmt.Errorf("expected map for message field, got %T", raw)
		}
		child := dynamicpb.NewMessage(fd.Message())
		if err := applyValue(child, nested); err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfMessage(child), nil
	case protoreflect.EnumKind:
		name, ok := raw.(string)
		if !ok {
			return protoreflect.Value{}, fmt.Errorf("expected string for enum, got %T", raw)
		}
		val := fd.Enum().Values().ByName(protoreflect.Name(name))
		if val == nil {
			return protoreflect.Value{}, fmt.Errorf("unknown enum value %q for %s", name, fd.Enum().FullName())
		}
		return protoreflect.ValueOfEnum(val.Number()), nil
	case protoreflect.BytesKind:
		switch b := raw.(type) {
		case []byte:
			return protoreflect.ValueOfBytes(b), nil
		case string:
			return protoreflect.ValueOfBytes([]byte(b)), nil
		}
		return protoreflect.Value{}, fmt.Errorf("expected bytes for %s, got %T", fd.Name(), raw)
	case protoreflect.StringKind:
		s, ok := raw.(string)
		if !ok {
			return protoreflect.Value{}, fmt.Errorf("expected string for %s, got %T", fd.Name(), raw)
		}
		return protoreflect.ValueOfString(s), nil
	case protoreflect.BoolKind:
		b, ok := raw.(bool)
		if !ok {
			return protoreflect.Value{}, fmt.Errorf("expected bool for %s, got %T", fd.Name(), raw)
		}
		return protoreflect.ValueOfBool(b), nil
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		n, err := numberToInt64(raw)
		if err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfInt32(int32(n)), nil
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		n, err := numberToInt64(raw)
		if err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfInt64(n), nil
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		n, err := numberToInt64(raw)
		if err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfUint32(uint32(n)), nil
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		n, err := numberToInt64(raw)
		if err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfUint64(uint64(n)), nil
	case protoreflect.FloatKind:
		n, err := numberToFloat64(raw)
		if err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfFloat32(float32(n)), nil
	case protoreflect.DoubleKind:
		n, err := numberToFloat64(raw)
		if err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfFloat64(n), nil
	}
	return protoreflect.Value{}, fmt.Errorf("unsupported proto kind %s for field %s", fd.Kind(), fd.Name())
}

func numberToInt64(raw any) (int64, error) {
	switch v := raw.(type) {
	case int:
		return int64(v), nil
	case int32:
		return int64(v), nil
	case int64:
		return v, nil
	case float64:
		return int64(v), nil
	case float32:
		return int64(v), nil
	}
	return 0, fmt.Errorf("expected numeric, got %T", raw)
}

func numberToFloat64(raw any) (float64, error) {
	switch v := raw.(type) {
	case float32:
		return float64(v), nil
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case int64:
		return float64(v), nil
	}
	return 0, fmt.Errorf("expected numeric, got %T", raw)
}
