package schemaregistry

import (
	"fmt"

	"github.com/hamba/avro/v2"
)

// avroHandler implements formatHandler for Avro.
//
// Encoding accepts either a typed Go struct (avro tags honoured by hamba/avro)
// or a generic map[string]any — the canonical shape produced by the orchestrator
// from JSON-shaped data generators.
//
// Decoding picks the target container based on the schema root:
//   - record       → map[string]any
//   - map          → map[string]any
//   - array        → []any
//   - primitives   → their natural Go type (string, int64, float64, bool, []byte…)
//   - union        → any (hamba resolves the branch)
//
// This is wider than the old "always a map" contract; a scenario that uses
// a top-level Avro string or array now round-trips correctly.
type avroHandler struct {
	schema avro.Schema
}

func newAvroHandler(text string) (formatHandler, error) {
	s, err := avro.Parse(text)
	if err != nil {
		return nil, fmt.Errorf("schemaregistry: parse avro schema: %w", err)
	}
	return &avroHandler{schema: s}, nil
}

func (h *avroHandler) encode(value any) ([]byte, error) {
	return avro.Marshal(h.schema, value)
}

func (h *avroHandler) decode(body []byte) (any, error) {
	switch h.schema.Type() {
	case avro.Record, avro.Map:
		out := make(map[string]any)
		if err := avro.Unmarshal(h.schema, body, &out); err != nil {
			return nil, err
		}
		return out, nil
	case avro.Array:
		var out []any
		if err := avro.Unmarshal(h.schema, body, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
	// Primitives, unions, enums, fixed, logical types — let hamba pick the
	// native Go container.
	var out any
	if err := avro.Unmarshal(h.schema, body, &out); err != nil {
		return nil, err
	}
	return out, nil
}
