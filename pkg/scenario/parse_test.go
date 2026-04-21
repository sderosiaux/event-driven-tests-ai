package scenario_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/event-driven-tests-ai/edt/pkg/scenario"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readTD(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return b
}

func TestParseMinimal(t *testing.T) {
	s, err := scenario.Parse(readTD(t, "minimal.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "edt.io/v1", s.APIVersion)
	assert.Equal(t, "Scenario", s.Kind)
	assert.Equal(t, "demo", s.Metadata.Name)
	require.NotNil(t, s.Spec.Connectors.Kafka)
	assert.Equal(t, "localhost:9092", s.Spec.Connectors.Kafka.BootstrapServers)
	assert.Empty(t, s.Spec.Steps)
}

func TestParseFull(t *testing.T) {
	s, err := scenario.Parse(readTD(t, "full.yaml"))
	require.NoError(t, err)

	// Metadata labels
	assert.Equal(t, "commerce", s.Metadata.Labels["team"])
	assert.Equal(t, "staging", s.Metadata.Labels["env"])

	// Kafka auth
	require.NotNil(t, s.Spec.Connectors.Kafka.Auth)
	assert.Equal(t, scenario.KafkaAuthSASLScram512, s.Spec.Connectors.Kafka.Auth.Type)
	assert.Equal(t, "alice", s.Spec.Connectors.Kafka.Auth.Username)

	// Schema registry
	require.NotNil(t, s.Spec.Connectors.Kafka.SchemaRegistry)
	assert.Equal(t, "confluent", s.Spec.Connectors.Kafka.SchemaRegistry.Flavor)

	// HTTP
	require.NotNil(t, s.Spec.Connectors.HTTP)
	assert.Equal(t, "https://api.example.com", s.Spec.Connectors.HTTP.BaseURL)
	require.NotNil(t, s.Spec.Connectors.HTTP.Auth)
	assert.Equal(t, "bearer", s.Spec.Connectors.HTTP.Auth.Type)

	// Data
	orders, ok := s.Spec.Data["orders"]
	require.True(t, ok, "expected data.orders key")
	assert.Equal(t, "faker", orders.Generator.Strategy)
	assert.Equal(t, int64(42), orders.Generator.Seed)
	assert.Equal(t, "${uuid()}", orders.Generator.Overrides["orderId"])

	// Steps
	require.Len(t, s.Spec.Steps, 3)
	assert.Equal(t, "place-order", s.Spec.Steps[0].Name)
	require.NotNil(t, s.Spec.Steps[0].Produce)
	assert.Equal(t, "orders", s.Spec.Steps[0].Produce.Topic)
	assert.InDelta(t, 0.02, s.Spec.Steps[0].Produce.FailRate, 1e-9)
	assert.Equal(t, "schema_violation", s.Spec.Steps[0].Produce.FailMode)

	require.NotNil(t, s.Spec.Steps[1].Consume)
	assert.Equal(t, "orders.ack", s.Spec.Steps[1].Consume.Topic)
	require.NotNil(t, s.Spec.Steps[1].Consume.SlowMode)
	assert.Equal(t, 100, s.Spec.Steps[1].Consume.SlowMode.PauseEvery)

	require.NotNil(t, s.Spec.Steps[2].HTTP)
	assert.Equal(t, "GET", s.Spec.Steps[2].HTTP.Method)

	// Checks
	require.Len(t, s.Spec.Checks, 2)
	assert.Equal(t, scenario.SeverityCritical, s.Spec.Checks[0].Severity)
	assert.Equal(t, "5m", s.Spec.Checks[0].Window)
}

func TestParseInvalidYAML(t *testing.T) {
	_, err := scenario.Parse(readTD(t, "invalid.yaml"))
	require.Error(t, err)
}

func TestParseEmpty(t *testing.T) {
	_, err := scenario.Parse(nil)
	require.Error(t, err)
}

func TestParseUnknownField(t *testing.T) {
	input := []byte(`
apiVersion: edt.io/v1
kind: Scenario
metadata:
  name: x
spec:
  connectors: {}
  steps: []
  whatIsThis: nope
`)
	_, err := scenario.Parse(input)
	require.Error(t, err, "KnownFields(true) should reject unknown fields")
}
