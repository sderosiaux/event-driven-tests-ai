package checks_test

import (
	"testing"
	"time"

	"github.com/event-driven-tests-ai/edt/pkg/checks"
	"github.com/event-driven-tests-ai/edt/pkg/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Codex finding §7 §7.1: http() operator must exist and support glob patterns.
func TestHTTPOperatorMatchesGlob(t *testing.T) {
	store := events.NewMemStore(0)
	t0 := time.Unix(0, 0)
	store.Append(events.Event{Stream: "http:/warehouse/orders/1", Ts: t0, Payload: map[string]any{"status": 200}, Direction: events.HTTPCall})
	store.Append(events.Event{Stream: "http:/warehouse/orders/2", Ts: t0, Payload: map[string]any{"status": 200}, Direction: events.HTTPCall})
	store.Append(events.Event{Stream: "http:/billing/charge", Ts: t0, Payload: map[string]any{"status": 200}, Direction: events.HTTPCall})

	e, err := checks.NewEvaluator(store)
	require.NoError(t, err)

	v, err := e.Evaluate("size(http('/warehouse/*'))")
	require.NoError(t, err)
	assert.Equal(t, int64(2), v)

	v, err = e.Evaluate("size(http('*'))")
	require.NoError(t, err)
	assert.Equal(t, int64(3), v)

	v, err = e.Evaluate("size(http('/missing'))")
	require.NoError(t, err)
	assert.Equal(t, int64(0), v)

	v, err = e.Evaluate("http('/warehouse/*').all(e, e.payload.status == 200)")
	require.NoError(t, err)
	assert.Equal(t, true, v)
}

func TestHTTPOperatorRateOfStatus(t *testing.T) {
	store := events.NewMemStore(0)
	t0 := time.Unix(0, 0)
	for i := 0; i < 9; i++ {
		store.Append(events.Event{Stream: "http:/api/x", Ts: t0, Payload: map[string]any{"status": 200}, Direction: events.HTTPCall})
	}
	store.Append(events.Event{Stream: "http:/api/x", Ts: t0, Payload: map[string]any{"status": 500}, Direction: events.HTTPCall})

	e, _ := checks.NewEvaluator(store)
	v, err := e.Evaluate("rate(http('/api/x').map(e, int(e.payload.status) == 200))")
	require.NoError(t, err)
	assert.InDelta(t, 0.9, v.(float64), 1e-9)
}
