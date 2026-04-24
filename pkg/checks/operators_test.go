package checks_test

import (
	"testing"
	"time"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLatency(t *testing.T) {
	t0 := time.Unix(0, 0)
	e := newEval(t, func(s *events.MemStore) {
		s.Append(events.Event{Stream: "orders", Key: "1", Ts: t0, Direction: events.Produced})
		s.Append(events.Event{Stream: "orders.ack", Key: "1", Ts: t0.Add(100 * time.Millisecond), Direction: events.Consumed})

		s.Append(events.Event{Stream: "orders", Key: "2", Ts: t0.Add(time.Second), Direction: events.Produced})
		s.Append(events.Event{Stream: "orders.ack", Key: "2", Ts: t0.Add(time.Second + 250*time.Millisecond), Direction: events.Consumed})

		// Unmatched: order with no ack
		s.Append(events.Event{Stream: "orders", Key: "3", Ts: t0.Add(2 * time.Second), Direction: events.Produced})
	})

	v, err := e.Evaluate("size(latency('orders', 'orders.ack'))")
	require.NoError(t, err)
	assert.Equal(t, int64(2), v, "expect 2 matched pairs out of 3 orders")

	// p99 with linear interp on 2 elements => max
	v, err = e.Evaluate("percentile(latency('orders', 'orders.ack'), 99) < duration('300ms')")
	require.NoError(t, err)
	assert.Equal(t, true, v)

	v, err = e.Evaluate("percentile(latency('orders', 'orders.ack'), 99) < duration('200ms')")
	require.NoError(t, err)
	assert.Equal(t, false, v)
}

func TestForallExistsViaCELMacros(t *testing.T) {
	t0 := time.Unix(0, 0)
	e := newEval(t, func(s *events.MemStore) {
		s.Append(events.Event{Stream: "orders", Key: "1", Ts: t0, Payload: map[string]any{"amount": 10.0}})
		s.Append(events.Event{Stream: "orders", Key: "2", Ts: t0, Payload: map[string]any{"amount": 20.0}})
		s.Append(events.Event{Stream: "orders", Key: "3", Ts: t0, Payload: map[string]any{"amount": 30.0}})
	})

	// All amounts > 5 → true
	v, err := e.Evaluate("stream('orders').all(e, e.payload.amount > 5.0)")
	require.NoError(t, err)
	assert.Equal(t, true, v)

	// Some amount > 25 → true
	v, err = e.Evaluate("stream('orders').exists(e, e.payload.amount > 25.0)")
	require.NoError(t, err)
	assert.Equal(t, true, v)

	// No amount > 100 → false
	v, err = e.Evaluate("stream('orders').exists(e, e.payload.amount > 100.0)")
	require.NoError(t, err)
	assert.Equal(t, false, v)
}

// Regression: codex P0 — one ack must not be claimed by multiple source events.
func TestLatencyEachAckUsedAtMostOnce(t *testing.T) {
	t0 := time.Unix(0, 0)
	e := newEval(t, func(s *events.MemStore) {
		// Two orders sharing a key, one ack — only one latency must be emitted.
		s.Append(events.Event{Stream: "orders", Key: "k", Ts: t0, Direction: events.Produced})
		s.Append(events.Event{Stream: "orders", Key: "k", Ts: t0.Add(10 * time.Millisecond), Direction: events.Produced})
		s.Append(events.Event{Stream: "orders.ack", Key: "k", Ts: t0.Add(50 * time.Millisecond), Direction: events.Consumed})
	})
	v, err := e.Evaluate("size(latency('orders', 'orders.ack'))")
	require.NoError(t, err)
	assert.Equal(t, int64(1), v, "second order must NOT also match the same ack")
}

// Regression: codex P0 — empty latency list must not silently pass SLAs.
func TestPercentileDurationEmptyErrors(t *testing.T) {
	e := newEval(t, nil)
	_, err := e.Evaluate("percentile(latency('orders', 'orders.ack'), 99)")
	require.Error(t, err, "percentile over an empty stream must surface an error, not 0s")
}

func TestPercentileDoubleEmptyErrors(t *testing.T) {
	e := newEval(t, nil)
	_, err := e.Evaluate("percentile([], 99)")
	require.Error(t, err)
}

func TestBefore(t *testing.T) {
	e := newEval(t, nil)
	v, err := e.Evaluate("before(timestamp('2026-01-01T00:00:00Z'), timestamp('2026-01-02T00:00:00Z'))")
	require.NoError(t, err)
	assert.Equal(t, true, v)

	v, err = e.Evaluate("before(timestamp('2026-01-02T00:00:00Z'), timestamp('2026-01-01T00:00:00Z'))")
	require.NoError(t, err)
	assert.Equal(t, false, v)
}

func TestNoOrphanCancellationsRealisticCheck(t *testing.T) {
	// Models the scenario from the design spec §6.2: "no_orphan_cancellations".
	t0 := time.Unix(0, 0)
	e := newEval(t, func(s *events.MemStore) {
		s.Append(events.Event{Stream: "orders", Key: "A", Ts: t0, Payload: map[string]any{"orderId": "A"}})
		s.Append(events.Event{Stream: "orders", Key: "B", Ts: t0, Payload: map[string]any{"orderId": "B"}})
		s.Append(events.Event{Stream: "cancellations", Key: "A", Ts: t0.Add(time.Second), Payload: map[string]any{"orderId": "A"}})
	})

	// Each cancellation must have a matching prior order with same orderId.
	expr := `stream('cancellations').all(c,
		stream('orders').exists(o, o.payload.orderId == c.payload.orderId && before(o.ts, c.ts))
	)`
	v, err := e.EvaluateBool(expr)
	require.NoError(t, err)
	assert.True(t, v)
}
