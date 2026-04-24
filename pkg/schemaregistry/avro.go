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
// Decoding always produces a map[string]any so downstream CEL checks see the
// same generic shape regardless of producer language.
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
	out := make(map[string]any)
	if err := avro.Unmarshal(h.schema, body, &out); err != nil {
		return nil, err
	}
	return out, nil
}
