package schemaregistry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// jsonHandler implements formatHandler for JSON Schema-validated payloads.
// Encoding validates the value against the schema then JSON-marshals it.
// Decoding JSON-unmarshals and re-validates so a malformed wire payload
// surfaces as a clear error rather than silently slipping through.
type jsonHandler struct {
	compiled *jsonschema.Schema
}

func newJSONHandler(text string) (formatHandler, error) {
	c := jsonschema.NewCompiler()
	doc, err := decodeJSONResource(text)
	if err != nil {
		return nil, err
	}
	if err := c.AddResource("schema.json", doc); err != nil {
		return nil, fmt.Errorf("schemaregistry: add json schema: %w", err)
	}
	compiled, err := c.Compile("schema.json")
	if err != nil {
		return nil, fmt.Errorf("schemaregistry: compile json schema: %w", err)
	}
	return &jsonHandler{compiled: compiled}, nil
}

func (h *jsonHandler) encode(value any) ([]byte, error) {
	if err := h.compiled.Validate(value); err != nil {
		return nil, fmt.Errorf("schemaregistry: json validate: %w", err)
	}
	return json.Marshal(value)
}

func (h *jsonHandler) decode(body []byte) (any, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if err := h.compiled.Validate(v); err != nil {
		return nil, fmt.Errorf("schemaregistry: json validate decoded: %w", err)
	}
	return v, nil
}

// decodeJSONResource accepts either a JSON document body or a string-wrapped
// schema (Confluent SR sometimes ships JSON Schema text inside a JSON string).
func decodeJSONResource(text string) (any, error) {
	t := strings.TrimSpace(text)
	if t == "" {
		return map[string]any{}, nil
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader([]byte(t)))
	dec.UseNumber()
	if err := dec.Decode(&v); err == nil {
		if s, ok := v.(string); ok {
			var inner any
			dec := json.NewDecoder(bytes.NewReader([]byte(s)))
			dec.UseNumber()
			if err := dec.Decode(&inner); err != nil {
				return nil, fmt.Errorf("schemaregistry: unwrap json schema string: %w", err)
			}
			return inner, nil
		}
		return v, nil
	}
	return nil, fmt.Errorf("schemaregistry: invalid json schema resource")
}
