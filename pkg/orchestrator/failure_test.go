package orchestrator_test

import (
	"context"
	"testing"
	"time"

	"github.com/event-driven-tests-ai/edt/pkg/events"
	"github.com/event-driven-tests-ai/edt/pkg/httpc"
	"github.com/event-driven-tests-ai/edt/pkg/kafka"
	"github.com/event-driven-tests-ai/edt/pkg/orchestrator"
	"github.com/event-driven-tests-ai/edt/pkg/scenario"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var httpcResp200 = httpc.Response{Status: 200}

func TestProduceFailRateHalfDeterministic(t *testing.T) {
	store := events.NewMemStore(0)
	fk := &fakeKafka{}
	r := orchestrator.New(&scenario.Scenario{}, fk, nil, store)

	// "timeout" mode skips the broker entirely so the count split is observable.
	s := &scenario.Scenario{Spec: scenario.Spec{Steps: []scenario.Step{{
		Name: "flaky",
		Produce: &scenario.ProduceStep{
			Topic: "orders", Payload: `{"k":1}`,
			Count: 200, FailRate: 0.5, FailMode: "timeout",
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
	assert.InDelta(t, 100, fails, 30, "fail rate ~50%% of 200")
	assert.Len(t, fk.produced, 200-fails, "successful produces must hit the fake kafka in timeout mode")
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

func TestProduceFailModeUnknownIsRejected(t *testing.T) {
	store := events.NewMemStore(0)
	r := orchestrator.New(&scenario.Scenario{}, &fakeKafka{}, nil, store)
	s := &scenario.Scenario{Spec: scenario.Spec{Steps: []scenario.Step{{
		Name:    "p",
		Produce: &scenario.ProduceStep{Topic: "t", Payload: `{}`, Count: 1, FailRate: 1, FailMode: "garbage"},
	}}}}
	err := r.Run(context.Background(), s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fail_mode")
}

func TestProduceSchemaViolationActuallyHitsBroker(t *testing.T) {
	store := events.NewMemStore(0)
	fk := &fakeKafka{}
	r := orchestrator.New(&scenario.Scenario{}, fk, nil, store)
	s := &scenario.Scenario{Spec: scenario.Spec{Steps: []scenario.Step{{
		Name: "p",
		Produce: &scenario.ProduceStep{
			Topic: "t", Payload: `{"orderId":"abc","amount":99.5}`,
			Count: 5, FailRate: 1.0, FailMode: "schema_violation",
		},
	}}}}
	require.NoError(t, r.Run(context.Background(), s))
	// schema_violation = corrupted bytes still hit the broker
	assert.Len(t, fk.produced, 5)
	for _, rec := range fk.produced {
		assert.Less(t, len(rec.Value), len(`{"orderId":"abc","amount":99.5}`), "value must be corrupted (truncated)")
	}
	for _, e := range store.Query("t") {
		assert.Equal(t, events.ProducedFailed, e.Direction)
		assert.Equal(t, "schema_violation", e.Payload.(map[string]any)["injected_failure"])
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

// Regression: codex P0 — HTTP fail_rate must actually inject failures.
func TestHTTPFailRateOneAlwaysInjects(t *testing.T) {
	hp := &fakeHTTP{} // would 500 if reached, but we should never reach it
	store := events.NewMemStore(0)
	r := orchestrator.New(&scenario.Scenario{}, nil, hp, store)

	s := &scenario.Scenario{Spec: scenario.Spec{Steps: []scenario.Step{{
		Name: "flaky-http",
		HTTP: &scenario.HTTPStep{
			Method: "GET", Path: "/x",
			FailRate: 1.0, FailMode: "http_5xx",
		},
	}}}}
	require.NoError(t, r.Run(context.Background(), s))
	require.Nil(t, hp.lastStep, "fail_rate=1 must skip the actual call")
	got := store.Query("http:/x")
	require.Len(t, got, 1)
	m := got[0].Payload.(map[string]any)
	assert.Equal(t, "http_5xx", m["injected_failure"])
	assert.Equal(t, 503, m["status"])
}

func TestHTTPFailRateZeroNeverInjects(t *testing.T) {
	hp := &fakeHTTP{next: &httpcResp200}
	store := events.NewMemStore(0)
	r := orchestrator.New(&scenario.Scenario{}, nil, hp, store)
	s := &scenario.Scenario{Spec: scenario.Spec{Steps: []scenario.Step{{
		Name: "stable-http",
		HTTP: &scenario.HTTPStep{Method: "GET", Path: "/x", FailRate: 0},
	}}}}
	require.NoError(t, r.Run(context.Background(), s))
	require.NotNil(t, hp.lastStep)
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
