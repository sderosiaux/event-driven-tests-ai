package scenario

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Parse decodes a scenario from YAML bytes. Unknown fields are rejected to
// catch typos early.
func Parse(b []byte) (*Scenario, error) {
	if len(b) == 0 {
		return nil, errors.New("scenario: empty input")
	}
	var s Scenario
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("scenario: yaml decode: %w", err)
	}
	return &s, nil
}

// ParseFile reads and decodes a scenario from a file path.
func ParseFile(path string) (*Scenario, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("scenario: read %q: %w", path, err)
	}
	return Parse(b)
}
