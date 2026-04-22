package orchestrator_test

import (
	"context"
	"testing"
	"time"

	"github.com/event-driven-tests-ai/edt/pkg/events"
	"github.com/event-driven-tests-ai/edt/pkg/kafka"
	"github.com/event-driven-tests-ai/edt/pkg/orchestrator"
	"github.com/event-driven-tests-ai/edt/pkg/scenario"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProduceFailRateHalfDeterministic(t *testing.T) {
	store := events.NewMemStore(0)
	fk := &fakeKafka{}
	r := orchestrator.New(&scenario.Scenario{}, fk, nil, store)

	s := &scenario.Scenario{Spec: scenario.Spec{Steps: []scenario.Step{{
		Name: "flaky",
		Produce: &scenario.ProduceStep{
			Topic: "orders", Payload: `{"k":1}`,
			Count: 200, FailRate: 0.5, FailMode: "schema_violation",
		},
	}}}}

	require.NoError(t, r.Run(context.Background(), s))

	all := store.Query("orders")
	require.Len(t, all, 200)

	fails := 0
	for _, e := range all {
		if e.Direction == events.ProducedFailed {
			fails++
		}
	}
	// Deterministic RNG: check result bound (loose, but tight enough to detect regression).
	assert.InDelta(t, 100, fails, 30, "fail rate ~50%% of 200")
	assert.Len(t, fk.produced, 200-fails, "successful produces must hit the fake kafka")
}

func TestProduceFailRateZeroNeverFails(t *testing.T) {
	store := events.NewMemStore(0)
	fk := &fakeKafka{}
	r := orchestrator.New(&scenario.Scenario{}, fk, nil, store)

	s := &scenario.Scenario{Spec: scenario.Spec{Steps: []scenario.Step{{
		Name: "never-fails",
		Produce: &scenario.ProduceStep{
			Topic: "t", Payload: `{"k":1}`, Count: 20, FailRate: 0,
		},
	}}}}
	require.NoError(t, r.Run(context.Background(), s))
	assert.Len(t, fk.produced, 20)
	for _, e := range store.Query("t") {
		assert.Equal(t, events.Produced, e.Direction)
	}
}

func TestProduceFailRateOneAlwaysFails(t *testing.T) {
	store := events.NewMemStore(0)
	fk := &fakeKafka{}
	r := orchestrator.New(&scenario.Scenario{}, fk, nil, store)

	s := &scenario.Scenario{Spec: scenario.Spec{Steps: []scenario.Step{{
		Name: "always-fails",
		Produce: &scenario.ProduceStep{
			Topic: "t", Payload: `{"k":1}`, Count: 5, FailRate: 1.0, FailMode: "broker_not_available",
		},
	}}}}
	require.NoError(t, r.Run(context.Background(), s))
	assert.Empty(t, fk.produced, "fail_rate=1 must not reach the broker")
	evs := store.Query("t")
	require.Len(t, evs, 5)
	for _, e := range evs {
		assert.Equal(t, events.ProducedFailed, e.Direction)
		m := e.Payload.(map[string]any)
		assert.Equal(t, "broker_not_available", m["injected_failure"])
	}
}

func TestConsumerSlowMode(t *testing.T) {
	now := time.Now()
	recs := make([]kafka.Record, 10)
	for i := range recs {
		recs[i] = kafka.Record{
			Topic: "orders.ack", Key: []byte("k"), Value: []byte(`{"n":1}`), Timestamp: now.UnixNano(),
		}
	}
	fk := &fakeKafka{consumeRecs: recs}
	store := events.NewMemStore(0)
	r := orchestrator.New(&scenario.Scenario{}, fk, nil, store)

	// A never-true match rule keeps the consumer reading until timeout, so
	// slow_mode pauses can accumulate over multiple records.
	s := &scenario.Scenario{Spec: scenario.Spec{Steps: []scenario.Step{{
		Name: "c",
		Consume: &scenario.ConsumeStep{
			Topic:   "orders.ack",
			Timeout: "500ms",
			Match:   []scenario.MatchRule{{Key: `payload.n == 9999`}}, // never matches
			SlowMode: &scenario.SlowMode{PauseEvery: 3, PauseFor: "80ms"},
		},
	}}}}

	t0 := time.Now()
	err := r.Run(context.Background(), s)
	elapsed := time.Since(t0)
	require.Error(t, err) // never-matching rule + timeout = error
	assert.Contains(t, err.Error(), "timed out")
	assert.GreaterOrEqual(t, elapsed, 200*time.Millisecond, "slow_mode should add observable lag")
	assert.Len(t, store.Query("orders.ack"), 10)
}
