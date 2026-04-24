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

// stubJudge returns deterministic scores keyed by the input's orderId so tests
// can assert aggregate math without hitting an LLM.
type stubJudge struct {
	scoreFor func(p eval.Pair) eval.Score
	calls    int
}

func (s *stubJudge) Score(_ context.Context, _ scenario.Eval, p eval.Pair) (eval.Score, error) {
	s.calls++
	return s.scoreFor(p), nil
}

func mkScenario() *scenario.Scenario {
	return &scenario.Scenario{
		Metadata: scenario.Metadata{Name: "triage"},
		Spec: scenario.Spec{
			AgentUnderTest: &scenario.AgentUnderTest{
				Name:     "agent",
				Consumes: []string{"orders.new"},
				Produces: []string{"orders.triaged"},
			},
			Evals: []scenario.Eval{
				{
					Name:      "triage_correctness",
					Judge:     &scenario.Judge{Model: "claude-opus-4-7", Rubric: "score triage"},
					Threshold: &scenario.EvalThreshold{Aggregate: "avg", Value: ">= 4.2"},
				},
			},
		},
	}
}

func TestExecutorPairsInputAndOutputOnMatchingKey(t *testing.T) {
	store := events.NewMemStore(0)
	now := time.Now()
	store.Append(events.Event{Stream: "orders.triaged", Key: "ord-1", Ts: now, Payload: map[string]any{"category": "refund"}, Direction: events.Produced})
	store.Append(events.Event{Stream: "orders.triaged", Key: "ord-2", Ts: now, Payload: map[string]any{"category": "ship"}, Direction: events.Produced})

	judge := &stubJudge{scoreFor: func(p eval.Pair) eval.Score {
		if p.Input["orderId"] == "ord-1" && p.Output["category"] == "refund" {
			return eval.Score{Value: 5}
		}
		if p.Input["orderId"] == "ord-2" && p.Output["category"] == "ship" {
			return eval.Score{Value: 4.5}
		}
		return eval.Score{Value: 1}
	}}

	exec := eval.New(eval.Config{Judge: judge, MatchTimeout: 50 * time.Millisecond})

	inputs := []events.Event{
		{Stream: "orders.new", Key: "ord-1", Payload: map[string]any{"orderId": "ord-1", "severity": "refund"}},
		{Stream: "orders.new", Key: "ord-2", Payload: map[string]any{"orderId": "ord-2", "severity": "ship"}},
	}
	require.NoError(t, exec.RunInputs(context.Background(), mkScenario(), store, inputs))

	results := exec.Finalize(mkScenario())
	require.Len(t, results, 1)
	r := results[0]
	assert.Equal(t, "triage_correctness", r.Eval)
	assert.Equal(t, 2, r.Over)
	assert.InDelta(t, 4.75, r.Value, 1e-9)
	assert.True(t, r.Passed, "avg 4.75 >= 4.2")
	assert.Equal(t, 2, judge.calls)
}

func TestExecutorMarksMissingOutputWithoutLosingTheEval(t *testing.T) {
	store := events.NewMemStore(0)
	var seenMissing bool
	judge := &stubJudge{scoreFor: func(p eval.Pair) eval.Score {
		if p.Output["_missing"] == true {
			seenMissing = true
			return eval.Score{Value: 1}
		}
		return eval.Score{Value: 5}
	}}
	exec := eval.New(eval.Config{Judge: judge, MatchTimeout: 30 * time.Millisecond})

	inputs := []events.Event{{Stream: "orders.new", Key: "ord-lost", Payload: map[string]any{"orderId": "ord-lost"}}}
	require.NoError(t, exec.RunInputs(context.Background(), mkScenario(), store, inputs))

	assert.True(t, seenMissing)
	r := exec.Finalize(mkScenario())
	require.Len(t, r, 1)
	assert.Equal(t, 1, r[0].Over)
	assert.False(t, r[0].Passed, "value 1 < 4.2 threshold")
}

func TestExecutorHonoursIterationCap(t *testing.T) {
	store := events.NewMemStore(0)
	judge := &stubJudge{scoreFor: func(eval.Pair) eval.Score { return eval.Score{Value: 4.5} }}
	exec := eval.New(eval.Config{Judge: judge, MatchTimeout: 10 * time.Millisecond, Iterations: 2})

	var inputs []events.Event
	for i := 0; i < 10; i++ {
		inputs = append(inputs, events.Event{Stream: "orders.new", Key: "k", Payload: map[string]any{}})
	}
	require.NoError(t, exec.RunInputs(context.Background(), mkScenario(), store, inputs))
	assert.Equal(t, 2, judge.calls)
}

func TestExecutorErrorsWithoutAgentUnderTest(t *testing.T) {
	exec := eval.New(eval.Config{Judge: &stubJudge{scoreFor: func(eval.Pair) eval.Score { return eval.Score{Value: 1} }}})
	err := exec.RunInputs(context.Background(), &scenario.Scenario{}, events.NewMemStore(0), nil)
	require.Error(t, err)
}

func TestExecutorOnScoreStreamsVerdicts(t *testing.T) {
	store := events.NewMemStore(0)
	store.Append(events.Event{Stream: "orders.triaged", Key: "k", Payload: map[string]any{"ok": true}, Direction: events.Produced})
	judge := &stubJudge{scoreFor: func(eval.Pair) eval.Score { return eval.Score{Value: 5} }}

	var streamed []eval.Score
	exec := eval.New(eval.Config{
		Judge:        judge,
		MatchTimeout: 50 * time.Millisecond,
		OnScore: func(name string, s eval.Score) {
			assert.Equal(t, "triage_correctness", name)
			streamed = append(streamed, s)
		},
	})
	require.NoError(t, exec.RunInputs(context.Background(), mkScenario(),
		store,
		[]events.Event{{Stream: "orders.new", Key: "k", Payload: map[string]any{}}},
	))
	assert.Len(t, streamed, 1)
	assert.InDelta(t, 5.0, streamed[0].Value, 1e-9)
}
