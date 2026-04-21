package scenario

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/invopop/jsonschema"
	jsv "github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// GenerateSchema returns the JSON Schema for a Scenario, as a *jsonschema.Schema.
// It is built by reflection over the Scenario struct tags at runtime.
func GenerateSchema() *jsonschema.Schema {
	r := &jsonschema.Reflector{
		DoNotReference:            true,  // inline everything for readability
		AllowAdditionalProperties: false, // strict — unknown keys rejected
		RequiredFromJSONSchemaTags: false,
	}
	return r.Reflect(&Scenario{})
}

// GenerateSchemaBytes returns the JSON Schema serialized as indented JSON.
func GenerateSchemaBytes() ([]byte, error) {
	s := GenerateSchema()
	return json.MarshalIndent(s, "", "  ")
}

// ValidateYAML parses a YAML scenario and validates it against the generated JSON Schema.
// It returns nil on success, otherwise an error describing the first violation.
func ValidateYAML(b []byte) error {
	// Convert YAML to a generic Go value, then to JSON for the validator.
	var y any
	if err := yaml.Unmarshal(b, &y); err != nil {
		return fmt.Errorf("scenario: yaml parse: %w", err)
	}
	y = normalizeForJSON(y)

	schemaBytes, err := GenerateSchemaBytes()
	if err != nil {
		return fmt.Errorf("scenario: generate schema: %w", err)
	}

	c := jsv.NewCompiler()
	if err := c.AddResource("edt-scenario.json", bytesToJSONValue(schemaBytes)); err != nil {
		return fmt.Errorf("scenario: load schema: %w", err)
	}
	compiled, err := c.Compile("edt-scenario.json")
	if err != nil {
		return fmt.Errorf("scenario: compile schema: %w", err)
	}

	if err := compiled.Validate(y); err != nil {
		return fmt.Errorf("scenario: validation failed: %w", err)
	}
	return nil
}

// bytesToJSONValue decodes JSON bytes into a generic Go value accepted by
// santhosh-tekuri/jsonschema/v6 as a resource.
func bytesToJSONValue(b []byte) any {
	var v any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	_ = dec.Decode(&v)
	return v
}

// normalizeForJSON converts map[any]any (common in yaml.v3 unmarshal) to
// map[string]any so that the JSON Schema validator accepts it.
func normalizeForJSON(v any) any {
	switch t := v.(type) {
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			out[fmt.Sprint(k)] = normalizeForJSON(vv)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			out[k] = normalizeForJSON(vv)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, vv := range t {
			out[i] = normalizeForJSON(vv)
		}
		return out
	default:
		return v
	}
}

// WriteSchema writes the JSON Schema to an io.Writer.
func WriteSchema(w io.Writer) error {
	b, err := GenerateSchemaBytes()
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}
