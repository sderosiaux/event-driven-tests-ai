package schemaregistry

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"time"
)

// magicByte is the leading byte of the Confluent wire format. Apicurio in CC
// compat mode emits the same byte. Native Apicurio v3 uses a different envelope
// (header-based) — out of M3 scope.
const magicByte = 0x00

// HeaderLen is the size of the wire-format prefix (magic + 4-byte schema ID).
const HeaderLen = 5

// EncodeHeader serializes the wire prefix for the given schema id.
func EncodeHeader(id int) []byte {
	out := make([]byte, HeaderLen)
	out[0] = magicByte
	binary.BigEndian.PutUint32(out[1:], uint32(id))
	return out
}

// ParseHeader returns the schema id encoded in the first 5 bytes of data.
// It returns an error when the magic byte does not match or the buffer is too
// short — either case means the producer did not use SR.
func ParseHeader(data []byte) (id int, body []byte, err error) {
	if len(data) < HeaderLen {
		return 0, nil, fmt.Errorf("schemaregistry: payload too short for SR header (%d bytes)", len(data))
	}
	if data[0] != magicByte {
		return 0, nil, fmt.Errorf("schemaregistry: bad magic byte 0x%02X (want 0x00)", data[0])
	}
	return int(binary.BigEndian.Uint32(data[1:HeaderLen])), data[HeaderLen:], nil
}

// Codec encodes and decodes payloads according to a schema fetched from the
// registry. One instance is bound to one Client; concurrent use is safe.
//
// The per-subject cache carries a TTL so "latest" can refresh when the
// registry advances that subject mid-run. The per-id cache is immutable —
// an id always resolves to the same schema body.
type Codec struct {
	cli   *Client
	ttl   time.Duration
	mu    sync.RWMutex
	byID  map[int]formatHandler // schema-id → handler (immutable)
	bySub map[string]idAndType  // subject → cached id+type+expiry for encode
}

type idAndType struct {
	id      int
	handler formatHandler
	expires time.Time // zero = never expires
}

// DefaultLatestTTL is how long the "latest" pointer for a subject stays cached
// before we re-query the registry. 30s balances "agent iterates fast" against
// "registry gets hammered". Override via NewCodecWithTTL when needed.
const DefaultLatestTTL = 30 * time.Second

// NewCodec builds a Codec on top of the given Client.
func NewCodec(c *Client) *Codec { return NewCodecWithTTL(c, DefaultLatestTTL) }

// NewCodecWithTTL is NewCodec with an explicit TTL for subject→latest cache
// entries. Set ttl to 0 to keep the entry forever (useful for tests).
func NewCodecWithTTL(c *Client, ttl time.Duration) *Codec {
	return &Codec{
		cli:   c,
		ttl:   ttl,
		byID:  make(map[int]formatHandler),
		bySub: make(map[string]idAndType),
	}
}

// formatHandler is the per-format strategy implementing encode/decode against
// a parsed schema. Each format (Avro, Protobuf, JSON Schema) registers itself
// here so the Codec stays format-agnostic.
type formatHandler interface {
	encode(value any) ([]byte, error)
	decode(body []byte) (any, error)
}

// Encode returns wire-format bytes for the given subject's latest schema.
// The subject's id and handler are cached on first use.
func (c *Codec) Encode(ctx context.Context, subject string, value any) ([]byte, error) {
	entry, err := c.handlerForSubject(ctx, subject)
	if err != nil {
		return nil, err
	}
	body, err := entry.handler.encode(value)
	if err != nil {
		return nil, fmt.Errorf("schemaregistry: encode subject %q: %w", subject, err)
	}
	return append(EncodeHeader(entry.id), body...), nil
}

// Decode parses a wire-format payload, fetching the referenced schema if it is
// not yet cached.
func (c *Codec) Decode(ctx context.Context, data []byte) (any, error) {
	id, body, err := ParseHeader(data)
	if err != nil {
		return nil, err
	}
	handler, err := c.handlerForID(ctx, id)
	if err != nil {
		return nil, err
	}
	out, err := handler.decode(body)
	if err != nil {
		return nil, fmt.Errorf("schemaregistry: decode schema id %d: %w", id, err)
	}
	return out, nil
}

func (c *Codec) handlerForSubject(ctx context.Context, subject string) (idAndType, error) {
	now := time.Now()
	c.mu.RLock()
	entry, ok := c.bySub[subject]
	c.mu.RUnlock()
	if ok && (entry.expires.IsZero() || now.Before(entry.expires)) {
		return entry, nil
	}

	schema, err := c.cli.GetLatestVersion(ctx, subject)
	if err != nil {
		return idAndType{}, err
	}
	h, err := buildHandler(schema)
	if err != nil {
		return idAndType{}, err
	}
	fresh := idAndType{id: schema.ID, handler: h}
	if c.ttl > 0 {
		fresh.expires = now.Add(c.ttl)
	}

	c.mu.Lock()
	c.bySub[subject] = fresh
	c.byID[schema.ID] = h
	c.mu.Unlock()
	return fresh, nil
}

func (c *Codec) handlerForID(ctx context.Context, id int) (formatHandler, error) {
	c.mu.RLock()
	if h, ok := c.byID[id]; ok {
		c.mu.RUnlock()
		return h, nil
	}
	c.mu.RUnlock()

	schema, err := c.cli.GetSchemaByID(ctx, id)
	if err != nil {
		return nil, err
	}
	h, err := buildHandler(schema)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.byID[id] = h
	c.mu.Unlock()
	return h, nil
}

// buildHandler dispatches on schema type. Adding a new format is a one-line
// change here plus a new file alongside avro.go.
func buildHandler(s Schema) (formatHandler, error) {
	switch s.Type {
	case TypeAvro, "":
		return newAvroHandler(s.Schema)
	case TypeJSON:
		return newJSONHandler(s.Schema)
	case TypeProtobuf:
		return newProtoHandler(s.Schema)
	}
	return nil, fmt.Errorf("schemaregistry: unsupported schema type %q", s.Type)
}
