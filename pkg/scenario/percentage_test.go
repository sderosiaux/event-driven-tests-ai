package scenario_test

import (
	"testing"

	"github.com/event-driven-tests-ai/edt/pkg/scenario"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestPercentageDecimal(t *testing.T) {
	var p scenario.Percentage
	require.NoError(t, yaml.Unmarshal([]byte("0.25"), &p))
	assert.InDelta(t, 0.25, p.Float64(), 1e-9)
}

// Codex finding: the contract uses `fail_rate: 2%` but the type rejected it.
func TestPercentageLiteral(t *testing.T) {
	cases := map[string]float64{
		"2%":     0.02,
		"50%":    0.5,
		"0.5%":   0.005,
		" 100% ": 1.0,
	}
	for input, want := range cases {
		var p scenario.Percentage
		require.NoError(t, yaml.Unmarshal([]byte(input), &p), "input=%q", input)
		assert.InDelta(t, want, p.Float64(), 1e-9, "input=%q", input)
	}
}

func TestPercentageOutOfRange(t *testing.T) {
	for _, bad := range []string{"-1", "1.5", "150%"} {
		var p scenario.Percentage
		require.Error(t, yaml.Unmarshal([]byte(bad), &p), "input=%q", bad)
	}
}
