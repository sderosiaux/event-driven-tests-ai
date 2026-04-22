package orchestrator_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/event-driven-tests-ai/edt/pkg/events"
	"github.com/event-driven-tests-ai/edt/pkg/orchestrator"
	"github.com/event-driven-tests-ai/edt/pkg/report"
	"github.com/event-driven-tests-ai/edt/pkg/scenario"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWatchEmitsSnapshotsOnInterval(t *testing.T) {
	store := events.NewMemStore(0)
	fk := &fakeKafka{}
	r := orchestrator.New(&scenario.Scenario{}, fk, nil, store)

	s := &scenario.Scenario{Spec: scenario.Spec{
		Steps: []scenario.Step{{
			Name:    "p",
			Produce: &scenario.ProduceStep{Topic: "orders", Payload: `{"x":1}`, Count: 1},
		}},
		Checks: []scenario.Check{
			{Name: "produced_at_least_one", Expr: "size(stream('orders')) >= 1", Severity: scenario.SeverityCritical},
		},
	}}

	var (
		mu       sync.Mutex
		reports  []*report.Report
		seenPass int32
	)

	cfg := orchestrator.WatchConfig{
		LoopInterval: 5 * time.Millisecond,
		EvalInterval: 50 * time.Millisecond,
		OnReport: func(rep *report.Report) {
			mu.Lock()
			reports = append(reports, rep)
			mu.Unlock()
			if rep.Status == report.StatusPass {
				atomic.AddInt32(&seenPass, 1)
			}
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	err := r.Watch(ctx, s, cfg)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.GreaterOrEqual(t, len(reports), 2, "expected multiple snapshots over the watch window")
	assert.GreaterOrEqual(t, atomic.LoadInt32(&seenPass), int32(1), "at least one snapshot should pass once events accumulate")
}

func TestWatchHonoursCheckWindow(t *testing.T) {
	store := events.NewMemStore(0)
	r := orchestrator.New(&scenario.Scenario{}, nil, nil, store)
	r.Store.Append(events.Event{Stream: "orders", Ts: time.Now().Add(-time.Hour), Key: "old"})
	r.Store.Append(events.Event{Stream: "orders", Ts: time.Now(), Key: "fresh"})

	s := &scenario.Scenario{Spec: scenario.Spec{
		Steps: []scenario.Step{}, // no producer, just observe pre-seeded store
		Checks: []scenario.Check{
			{Name: "fresh_only", Expr: "size(stream('orders')) == 1", Window: "10s", Severity: scenario.SeverityCritical},
		},
	}}

	var captured *report.Report
	cfg := orchestrator.WatchConfig{
		LoopInterval:  10 * time.Millisecond,
		EvalInterval:  20 * time.Millisecond,
		DefaultWindow: time.Hour, // would make the check see both events
		OnReport: func(rep *report.Report) {
			if rep != nil && len(rep.Checks) > 0 {
				captured = rep
			}
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	require.NoError(t, r.Watch(ctx, s, cfg))

	require.NotNil(t, captured, "at least one snapshot expected")
	assert.True(t, captured.Checks[0].Passed, "10s window must hide the hour-old event so size==1")
}
