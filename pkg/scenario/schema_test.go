package scenario_test

import (
	"encoding/json"
	"testing"

	"github.com/event-driven-tests-ai/edt/pkg/scenario"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateSchemaIsJSON(t *testing.T) {
	b, err := scenario.GenerateSchemaBytes()
	require.NoError(t, err)
	require.NotEmpty(t, b)

	var v map[string]any
	require.NoError(t, json.Unmarshal(b, &v))

	// Sanity: root must have the usual JSON Schema keys.
	_, hasProps := v["properties"]
	assert.True(t, hasProps, "root schema should have properties")
}

func TestValidateValidMinimal(t *testing.T) {
	err := scenario.ValidateYAML(readTD(t, "minimal.yaml"))
	require.NoError(t, err)
}

func TestValidateValidFull(t *testing.T) {
	err := scenario.ValidateYAML(readTD(t, "full.yaml"))
	require.NoError(t, err)
}

func TestValidateRejectsMissingRequired(t *testing.T) {
	// No apiVersion field.
	input := []byte(`
kind: Scenario
metadata: { name: x }
spec:
  connectors: {}
  steps: []
`)
	err := scenario.ValidateYAML(input)
	require.Error(t, err)
}

func TestValidateRejectsUnknownRootField(t *testing.T) {
	input := []byte(`
apiVersion: edt.io/v1
kind: Scenario
metadata: { name: x }
spec:
  connectors: {}
  steps: []
extraneous: true
`)
	err := scenario.ValidateYAML(input)
	require.Error(t, err)
}

func TestValidateAcceptsPercentageLiteral(t *testing.T) {
	input := []byte(`
apiVersion: edt.io/v1
kind: Scenario
metadata: { name: x }
spec:
  connectors: {}
  steps:
    - name: p
      produce:
        topic: orders
        payload: "{}"
        fail_rate: 2%
`)
	require.NoError(t, scenario.ValidateYAML(input))
}
