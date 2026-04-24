package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/event-driven-tests-ai/edt/pkg/events"
	"github.com/event-driven-tests-ai/edt/pkg/orchestrator"
	"github.com/event-driven-tests-ai/edt/pkg/scenario"
)

// Config parameterises an Executor.
type Config struct {
	// Iterations caps how many (input, output) pairs an eval processes before
	// aggregating. 0 = unbounded (ctx deadline is the only guard).
	Iterations int

	// MatchTimeout is how long a single input waits for its matching output.
	MatchTimeout time.Duration

	// Judge is the verdict source. Required.
	Judge Judge

	// OnScore fires for every finalized score (live streaming for the UI).
	OnScore func(evalName string, score Score)
}

func (c *Config) defaults() {
	if c.MatchTimeout <= 0 {
		c.MatchTimeout = 10 * time.Second
	}
	if c.OnScore == nil {
		c.OnScore = func(string, Score) {}
	}
}

// Result summarises the aggregate for one eval across all iterations.
type Result struct {
	Eval      string  `json:"eval"`
	Aggregate string  `json:"aggregate"`
	Over      int     `json:"over_samples"`
	Value     float64 `json:"value"`
	Threshold string  `json:"threshold,omitempty"`
	Passed    bool    `json:"passed"`
	Errors    int     `json:"errors"`
}

// Executor drives the eval harness. Use Run for a canned loop, or call
// Observe/Finalize yourself if you drive the pairing externally.
type Executor struct {
	cfg Config
	mu  sync.Mutex
	agg map[string]*Aggregate // eval name → aggregate
	err map[string]int        // eval name → judge-error count
}

func New(cfg Config) *Executor {
	cfg.defaults()
	return &Executor{cfg: cfg, agg: map[string]*Aggregate{}, err: map[string]int{}}
}

// Observe scores a single pair against every eval that has a judge block.
// Pure-CEL evals (Expr without Judge) are left to the existing checks package
// and ignored here.
func (e *Executor) Observe(ctx context.Context, s *scenario.Scenario, p Pair) {
	for _, ev := range s.Spec.Evals {
		if ev.Judge == nil {
			continue
		}
		e.scoreOne(ctx, ev, p)
	}
}

func (e *Executor) scoreOne(ctx context.Context, ev scenario.Eval, p Pair) {
	score, err := e.cfg.Judge.Score(ctx, ev, p)
	if err != nil {
		score = Score{Err: err}
	}

	e.mu.Lock()
	a, ok := e.agg[ev.Name]
	if !ok {
		kind := "avg"
		if ev.Threshold != nil && ev.Threshold.Aggregate != "" {
			kind = ev.Threshold.Aggregate
		}
		a = NewAggregate(kind)
		e.agg[ev.Name] = a
	}
	a.Add(score)
	if score.Err != nil {
		e.err[ev.Name]++
	}
	e.mu.Unlock()

	e.cfg.OnScore(ev.Name, score)
}

// Finalize reduces each aggregate, compares to its threshold, and returns one
// Result per eval with a judge block. Pure-CEL evals are dropped — callers
// handle those via pkg/checks.
func (e *Executor) Finalize(s *scenario.Scenario) []Result {
	e.mu.Lock()
	defer e.mu.Unlock()

	out := make([]Result, 0, len(s.Spec.Evals))
	for _, ev := range s.Spec.Evals {
		if ev.Judge == nil {
			continue
		}
		a, ok := e.agg[ev.Name]
		if !ok {
			a = NewAggregate("avg")
		}
		r := Result{
			Eval:      ev.Name,
			Aggregate: a.Kind,
			Over:      a.Count(),
			Value:     a.Value(),
			Errors:    e.err[ev.Name],
		}
		if ev.Threshold != nil {
			r.Threshold = ev.Threshold.Value
			passed, err := CheckThreshold(a.Value(), ev.Threshold.Value)
			r.Passed = err == nil && passed
		}
		out = append(out, r)
	}
	return out
}

// ---- canned runner ---------------------------------------------------------

// RunInputs is the default iteration loop: for each input event observed on
// the agent-under-test's consume topic, wait up to MatchTimeout for the
// agent's reply on a produce topic keyed the same way, then score.
//
// This is a synchronous convenience wrapper. Callers with more exotic match
// topologies (N inputs → M outputs, temporal joins) should call Observe
// directly from their own correlator.
func (e *Executor) RunInputs(ctx context.Context, s *scenario.Scenario, store events.Store, inputs []events.Event) error {
	if s.Spec.AgentUnderTest == nil {
		return fmt.Errorf("eval: scenario.spec.agent_under_test is required")
	}
	outStreams := s.Spec.AgentUnderTest.Produces
	if len(outStreams) == 0 {
		return fmt.Errorf("eval: agent_under_test must declare at least one produces topic")
	}

	for i, in := range inputs {
		if e.cfg.Iterations > 0 && i >= e.cfg.Iterations {
			break
		}
		out, ok := waitForOutput(ctx, store, outStreams, in.Key, e.cfg.MatchTimeout)
		if !ok {
			e.Observe(ctx, s, Pair{
				Input:  asMap(in.Payload),
				Output: map[string]any{"_missing": true, "_reason": "no matching output within timeout"},
				Meta:   map[string]any{"scenario": s.Metadata.Name, "iteration": i},
			})
			continue
		}
		e.Observe(ctx, s, Pair{
			Input:  asMap(in.Payload),
			Output: asMap(out.Payload),
			Meta:   map[string]any{"scenario": s.Metadata.Name, "iteration": i},
		})
	}
	return nil
}

// waitForOutput polls the store every 50ms for any event in outStreams whose
// key matches. Small-poll is fine — the store is in-memory and the executor
// is the only writer-serialization boundary.
func waitForOutput(ctx context.Context, store events.Store, streams []string, key string, timeout time.Duration) (events.Event, bool) {
	deadline := time.Now().Add(timeout)
	for {
		for _, name := range streams {
			for _, e := range store.Query(name) {
				if e.Key == key {
					return e, true
				}
			}
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return events.Event{}, false
		}
		select {
		case <-time.After(50 * time.Millisecond):
		case <-ctx.Done():
			return events.Event{}, false
		}
	}
}

// asMap normalises a payload to map[string]any. JSON-encoded strings are
// decoded best-effort; non-map scalars are wrapped under "_value".
func asMap(v any) map[string]any {
	switch t := v.(type) {
	case map[string]any:
		return t
	case []byte:
		var out map[string]any
		if err := json.Unmarshal(t, &out); err == nil {
			return out
		}
		return map[string]any{"_raw": string(t)}
	case string:
		var out map[string]any
		if err := json.Unmarshal([]byte(t), &out); err == nil {
			return out
		}
		return map[string]any{"_value": t}
	}
	return map[string]any{"_value": v}
}

// Silence unused-import lint if scenario types shift away from orchestrator
// at compile time.
var _ = orchestrator.New
