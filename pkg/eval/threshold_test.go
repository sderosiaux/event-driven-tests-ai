package eval_test

import (
	"context"
	"testing"
	"time"

	"github.com/event-driven-tests-ai/edt/pkg/eval"
	"github.com/event-driven-tests-ai/edt/pkg/events"
	"github.com/event-driven-tests-ai/edt/pkg/scenario"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func scenarioWithOver(over string) *scenario.Scenario {
	s := mkScenario()
	s.Spec.Evals[0].Threshold = &scenario.EvalThreshold{Aggregate: "avg", Value: ">= 4.2", Over: over}
	return s
}

// Codex P0 #4: required-sample minima must actually be enforced.
func TestThresholdOverEnforcesMinimumSamples(t *testing.T) {
	store := events.NewMemStore(0)
	now := time.Now()
	store.Append(events.Event{Stream: "orders.triaged", Key: "k", Ts: now, Payload: map[string]any{"category": "refund"}, Direction: events.Produced})

	judge := &stubJudge{scoreFor: func(eval.Pair) eval.Score { return eval.Score{Value: 5.0} }}
	exec := eval.New(eval.Config{Judge: judge, MatchTimeout: 20 * time.Millisecond})

	s := scenarioWithOver("50 runs")
	inputs := []events.Event{{Stream: "orders.new", Key: "k", Payload: map[string]any{"orderId": "k"}}}
	require.NoError(t, exec.RunInputs(context.Background(), s, store, inputs))

	r := exec.Finalize(s)
	require.Len(t, r, 1)
	assert.Equal(t, "insufficient_samples", r[0].Status)
	assert.False(t, r[0].Passed, "single sample cannot satisfy '50 runs'")
	assert.Equal(t, 50, r[0].RequiredOver)
}

// Codex P0 #4: when the required count is met the threshold is checked.
func TestThresholdOverSatisfiedPassesWhenValueClears(t *testing.T) {
	store := events.NewMemStore(0)
	now := time.Now()
	for i := 0; i < 3; i++ {
		store.Append(events.Event{Stream: "orders.triaged", Key: "k", Ts: now.Add(time.Duration(i) * time.Millisecond), Payload: map[string]any{}, Direction: events.Produced})
	}
	judge := &stubJudge{scoreFor: func(eval.Pair) eval.Score { return eval.Score{Value: 4.5} }}
	exec := eval.New(eval.Config{Judge: judge, MatchTimeout: 30 * time.Millisecond})

	s := scenarioWithOver("3 runs")
	inputs := make([]events.Event, 3)
	for i := range inputs {
		inputs[i] = events.Event{Stream: "orders.new", Key: "k", Payload: map[string]any{}}
	}
	require.NoError(t, exec.RunInputs(context.Background(), s, store, inputs))

	r := exec.Finalize(s)
	require.Len(t, r, 1)
	assert.Equal(t, "pass", r[0].Status)
	assert.True(t, r[0].Passed)
}

// Codex P0 #5: a run that is all-errors cannot silently pass.
func TestAllErrorsDoNotSilentlyPass(t *testing.T) {
	store := events.NewMemStore(0)
	store.Append(events.Event{Stream: "orders.triaged", Key: "k", Payload: map[string]any{}, Direction: events.Produced})

	failingJudge := &failingJudge{err: assert.AnError}
	exec := eval.New(eval.Config{Judge: failingJudge, MatchTimeout: 20 * time.Millisecond})

	s := mkScenario()
	inputs := []events.Event{{Stream: "orders.new", Key: "k", Payload: map[string]any{}}}
	require.NoError(t, exec.RunInputs(context.Background(), s, store, inputs))

	r := exec.Finalize(s)
	require.Len(t, r, 1)
	assert.Equal(t, "judge_errors", r[0].Status)
	assert.False(t, r[0].Passed)
	assert.Equal(t, 1, r[0].Errors)
	assert.Equal(t, 0, r[0].Over)
}

type failingJudge struct{ err error }

func (f *failingJudge) Score(_ context.Context, _ scenario.Eval, _ eval.Pair) (eval.Score, error) {
	return eval.Score{}, f.err
}
