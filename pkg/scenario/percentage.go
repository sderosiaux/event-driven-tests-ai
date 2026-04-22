package scenario

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/invopop/jsonschema"
	"gopkg.in/yaml.v3"
)

// Percentage is a fractional value in [0, 1] that accepts both decimal and
// percent-literal YAML representations. Both of these unmarshal the same:
//
//	fail_rate: 0.02
//	fail_rate: 2%
//
// It serializes as its underlying float64 so JSON Schema and JSON reports
// remain simple numbers.
type Percentage float64

func (p *Percentage) UnmarshalYAML(node *yaml.Node) error {
	s := strings.TrimSpace(node.Value)
	if s == "" {
		*p = 0
		return nil
	}
	if strings.HasSuffix(s, "%") {
		raw := strings.TrimSpace(strings.TrimSuffix(s, "%"))
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fmt.Errorf("percentage: invalid numeric part of %q: %w", s, err)
		}
		*p = Percentage(v / 100.0)
		return bounded(*p)
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("percentage: invalid %q: %w", s, err)
	}
	*p = Percentage(v)
	return bounded(*p)
}

func (p Percentage) MarshalYAML() (any, error) { return float64(p), nil }

// Float64 returns the fractional value as a float in [0, 1].
func (p Percentage) Float64() float64 { return float64(p) }

// JSONSchema keeps the generated schema aligned with UnmarshalYAML: callers may
// provide either a fractional number or a percent-literal string such as "2%".
func (Percentage) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		OneOf: []*jsonschema.Schema{
			{
				Type:    "number",
				Minimum: json.Number("0"),
				Maximum: json.Number("1"),
			},
			{
				Type:    "string",
				Pattern: `^\s*\d+(?:\.\d+)?%\s*$`,
			},
		},
	}
}

func bounded(p Percentage) error {
	if p < 0 || p > 1 {
		return fmt.Errorf("percentage %g out of [0, 1]", float64(p))
	}
	return nil
}
