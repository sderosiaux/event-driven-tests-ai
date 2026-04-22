package checks_test

import (
	"testing"
	"time"

	"github.com/event-driven-tests-ai/edt/pkg/checks"
	"github.com/event-driven-tests-ai/edt/pkg/events"
	"github.com/event-driven-tests-ai/edt/pkg/scenario"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluateAllMixedResults(t *testing.T) {
	t0 := time.Unix(0, 0)
	store := events.NewMemStore(0)
	store.Append(events.Event{Stream: "orders", Key: "1", Ts: t0, Payload: map[string]any{"amount": 100.0}})
	store.Append(events.Event{Stream: "orders.ack", Key: "1", Ts: t0.Add(50 * time.Millisecond)})

	e, err := checks.NewEvaluator(store)
	require.NoError(t, err)

	cs := []scenario.Check{
		{Name: "ack_fast", Expr: "percentile(latency('orders', 'orders.ack'), 99) < duration('200ms')", Severity: scenario.SeverityCritical},
		{Name: "ack_too_fast", Expr: "percentile(latency('orders', 'orders.ack'), 99) < duration('1ms')", Severity: scenario.SeverityCritical},
		{Name: "non_bool", Expr: "1 + 2", Severity: scenario.SeverityWarning},
		{Name: "compile_error", Expr: "this is bogus", Severity: scenario.SeverityWarning},
	}

	results := checks.EvaluateAll(e, cs)
	require.Len(t, results, 4)

	assert.True(t, results[0].Passed, "ack_fast should pass: 50ms < 200ms")
	assert.False(t, results[0].Failed())

	assert.False(t, results[1].Passed, "ack_too_fast should fail: 50ms not < 1ms")
	assert.True(t, results[1].IsCritical())

	assert.NotEmpty(t, results[2].Err, "non-bool result must surface error")
	assert.NotEmpty(t, results[3].Err, "compile error must surface error")
	assert.False(t, results[3].IsCritical(), "warning severity errors are not critical")

	assert.True(t, checks.AnyCriticalFailed(results))
}

func TestSeverityDefaultsToWarning(t *testing.T) {
	store := events.NewMemStore(0)
	e, _ := checks.NewEvaluator(store)
	cs := []scenario.Check{{Name: "x", Expr: "true"}}
	r := checks.EvaluateAll(e, cs)
	assert.Equal(t, scenario.SeverityWarning, r[0].Severity)
	assert.True(t, r[0].Passed)
}

func TestNoCriticalWhenAllPass(t *testing.T) {
	store := events.NewMemStore(0)
	e, _ := checks.NewEvaluator(store)
	cs := []scenario.Check{
		{Name: "always_true", Expr: "true", Severity: scenario.SeverityCritical},
	}
	r := checks.EvaluateAll(e, cs)
	assert.False(t, checks.AnyCriticalFailed(r))
}
