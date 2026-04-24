package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/events"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/orchestrator"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
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
	Eval         string  `json:"eval"`
	Aggregate    string  `json:"aggregate"`
	Over         int     `json:"over_samples"`
	RequiredOver int     `json:"required_samples,omitempty"` // from scenario.threshold.over "<N> runs"
	Value        float64 `json:"value"`
	Threshold    string  `json:"threshold,omitempty"`
	Passed       bool    `json:"passed"`
	Status       string  `json:"status"` // pass | fail | insufficient_samples | judge_errors
	Errors       int     `json:"errors"`
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
			r.RequiredOver = parseMinSamples(ev.Threshold.Over)
		}
		r.Passed, r.Status = finalizeStatus(r, ev)
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
	if err := validateAgent(s); err != nil {
		return err
	}
	outStreams := s.Spec.AgentUnderTest.Produces

	used := make(map[string]map[int]struct{}, len(outStreams))
	noLowerBound := map[string]int{}
	for i, in := range inputs {
		if e.cfg.Iterations > 0 && i >= e.cfg.Iterations {
			break
		}
		if err := e.scorePair(ctx, s, store, in, outStreams, noLowerBound, used, i); err != nil {
			return err
		}
	}
	return nil
}

// RunIterations drives `iterations` rounds of: reseed → pair newly-seeded
// inputs with matching outputs → judge. This is what "--iterations N" in the
// CLI should give you: N distinct samples per eval, not N capped by a single
// seeding batch.
//
// reseed is expected to call the scenario's produce steps. It runs once per
// iteration; the executor only consumes events that appeared after the
// snapshot taken before the reseed call, so previously-judged inputs are not
// re-scored.
func (e *Executor) RunIterations(ctx context.Context, s *scenario.Scenario, store events.Store, reseed func(ctx context.Context) error, iterations int) error {
	if err := validateAgent(s); err != nil {
		return err
	}
	if reseed == nil {
		return fmt.Errorf("eval: reseed callback is required for RunIterations")
	}
	if iterations <= 0 {
		iterations = 1
	}
	inStreams := s.Spec.AgentUnderTest.Consumes
	outStreams := s.Spec.AgentUnderTest.Produces
	used := make(map[string]map[int]struct{}, len(outStreams))

	for iter := 0; iter < iterations; iter++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Snapshot cursors on BOTH input and output streams before reseeding.
		// waitForOutput then ignores any pre-iteration events, so a stale
		// output from a previous iteration can never spuriously satisfy a
		// newly seeded input that happens to share a key.
		inCursors := snapshotCursors(store, inStreams)
		outCursors := snapshotCursors(store, outStreams)

		if err := reseed(ctx); err != nil {
			return fmt.Errorf("eval iteration %d: reseed: %w", iter, err)
		}
		fresh := freshInputsAfter(store, inStreams, inCursors)
		if len(fresh) == 0 {
			continue
		}
		for _, in := range fresh {
			if err := e.scorePair(ctx, s, store, in, outStreams, outCursors, used, iter); err != nil {
				return err
			}
		}
	}
	return nil
}

// scorePair runs one input through matching + judging. A real ctx cancel
// surfaces as an error so RunIterations aborts instead of recording the whole
// remaining backlog as missing-output samples.
func (e *Executor) scorePair(ctx context.Context, s *scenario.Scenario, store events.Store, in events.Event, outStreams []string, lowerBounds map[string]int, used map[string]map[int]struct{}, iter int) error {
	out, status := waitForOutput(ctx, store, outStreams, in.Key, e.cfg.MatchTimeout, lowerBounds, used)
	switch status {
	case matchHit:
		e.Observe(ctx, s, Pair{
			Input:  asMap(in.Payload),
			Output: asMap(out.Payload),
			Meta:   map[string]any{"scenario": s.Metadata.Name, "iteration": iter},
		})
		return nil
	case matchTimeout:
		e.Observe(ctx, s, Pair{
			Input:  asMap(in.Payload),
			Output: map[string]any{"_missing": true, "_reason": "no matching output within timeout"},
			Meta:   map[string]any{"scenario": s.Metadata.Name, "iteration": iter},
		})
		return nil
	case matchCancelled:
		return ctx.Err()
	}
	return nil
}

func snapshotCursors(st events.Store, streams []string) map[string]int {
	out := make(map[string]int, len(streams))
	for _, topic := range streams {
		out[topic] = len(st.Query(topic))
	}
	return out
}

func validateAgent(s *scenario.Scenario) error {
	if s.Spec.AgentUnderTest == nil {
		return fmt.Errorf("eval: scenario.spec.agent_under_test is required")
	}
	if len(s.Spec.AgentUnderTest.Produces) == 0 {
		return fmt.Errorf("eval: agent_under_test must declare at least one produces topic")
	}
	return nil
}

func freshInputsAfter(st events.Store, streams []string, cursors map[string]int) []events.Event {
	var out []events.Event
	for _, topic := range streams {
		all := st.Query(topic)
		start := cursors[topic]
		if start >= len(all) {
			continue
		}
		out = append(out, all[start:]...)
	}
	return out
}

// matchStatus distinguishes the three outcomes waitForOutput can return: a
// successful match, a real timeout (record as missing), or context cancel
// (abort the whole run, do not poison the aggregate).
type matchStatus int

const (
	matchHit matchStatus = iota
	matchTimeout
	matchCancelled
)

// waitForOutput polls the store every 50ms for any event in outStreams whose
// key matches. lowerBounds[stream] is the per-iteration "ignore everything
// before this index" cursor so stale outputs from previous iterations can't
// satisfy newly seeded inputs that share a key.
//
// Small-poll is fine — the store is in-memory and the executor is the only
// writer-serialization boundary.
func waitForOutput(ctx context.Context, store events.Store, streams []string, key string, timeout time.Duration, lowerBounds map[string]int, used map[string]map[int]struct{}) (events.Event, matchStatus) {
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return events.Event{}, matchCancelled
		}
		for _, name := range streams {
			seen := used[name]
			start := lowerBounds[name]
			all := store.Query(name)
			for idx := start; idx < len(all); idx++ {
				if _, ok := seen[idx]; ok {
					continue
				}
				e := all[idx]
				if e.Key == key {
					if seen == nil {
						seen = map[int]struct{}{}
						used[name] = seen
					}
					seen[idx] = struct{}{}
					return e, matchHit
				}
			}
		}
		if time.Now().After(deadline) {
			return events.Event{}, matchTimeout
		}
		select {
		case <-time.After(50 * time.Millisecond):
		case <-ctx.Done():
			return events.Event{}, matchCancelled
		}
	}
}

// maxPreviewLen bounds how much untrusted text from a malformed payload gets
// forwarded into the LLM prompt. A hostile producer who ships MB-sized
// payloads cannot burn our context budget or use the payload as a Trojan
// for prompt-injection at this size.
const maxPreviewLen = 512

// asMap normalises a payload to map[string]any. Well-formed JSON maps pass
// through; anything else is wrapped with _raw or _value but truncated and
// fingerprinted so the judge sees "untrusted opaque bytes" rather than
// arbitrary attacker-controlled prose.
func asMap(v any) map[string]any {
	switch t := v.(type) {
	case map[string]any:
		return t
	case []byte:
		var out map[string]any
		if err := json.Unmarshal(t, &out); err == nil {
			return out
		}
		return untrustedBlob("_raw", string(t))
	case string:
		var out map[string]any
		if err := json.Unmarshal([]byte(t), &out); err == nil {
			return out
		}
		return untrustedBlob("_value", t)
	}
	return map[string]any{"_value": v}
}

// untrustedBlob replaces a raw payload with a preview-safe map: the first
// maxPreviewLen bytes (JSON-quoted), the byte length, and a sha256 prefix
// so two identical corrupt payloads collapse to the same fingerprint.
func untrustedBlob(field, text string) map[string]any {
	preview := text
	truncated := false
	if len(preview) > maxPreviewLen {
		preview = preview[:maxPreviewLen]
		truncated = true
	}
	// Escape every byte that would look like an instruction in the prompt
	// (angle brackets, backticks, code fences). json.Marshal handles quoting.
	encoded, _ := json.Marshal(preview)
	return map[string]any{
		field:         string(encoded), // always a quoted JSON literal
		"_bytes":      len(text),
		"_truncated":  truncated,
		"_fingerprint": fingerprint(text),
		"_untrusted":  true,
	}
}

func fingerprint(text string) string {
	sum := sha256Prefix([]byte(text))
	return "sha256:" + sum
}

// parseMinSamples extracts the numeric target from a scenario.threshold.over
// expression like "50 runs". Returns 0 when absent or unparseable — a 0
// required-count degrades gracefully to "no minimum". Duration-based "over"
// ("1h", "5m") is out of scope for this helper and is ignored (returns 0).
func parseMinSamples(over string) int {
	over = strings.TrimSpace(strings.ToLower(over))
	if over == "" {
		return 0
	}
	// Split on whitespace; first field is the number, second (if any) is the unit.
	fields := strings.Fields(over)
	if len(fields) == 0 {
		return 0
	}
	n, err := strconv.Atoi(fields[0])
	if err != nil || n <= 0 {
		return 0
	}
	// Only the "runs" / "samples" unit is honoured as a hard minimum. A
	// duration expression ("1h") is observational only — a watch-mode
	// scheduler knows about wall-clock; the eval executor does not.
	if len(fields) > 1 {
		unit := fields[1]
		if unit != "runs" && unit != "samples" && unit != "run" && unit != "sample" {
			return 0
		}
	}
	return n
}

// finalizeStatus decides the eval's pass/fail posture. It folds three signals
// that used to be lost: observed sample count vs required, judge-error count,
// and the threshold comparison. A threshold is only tested after the
// minimum-samples gate and the all-errors gate have passed.
func finalizeStatus(r Result, ev scenario.Eval) (bool, string) {
	// All observations errored → eval cannot be judged.
	if r.Over == 0 && r.Errors > 0 {
		return false, "judge_errors"
	}
	// No samples at all → nothing to judge.
	if r.Over == 0 {
		return false, "insufficient_samples"
	}
	// Explicit minimum declared, not reached.
	if r.RequiredOver > 0 && r.Over < r.RequiredOver {
		return false, "insufficient_samples"
	}
	if ev.Threshold == nil || ev.Threshold.Value == "" {
		return true, "pass"
	}
	passed, err := CheckThreshold(r.Value, ev.Threshold.Value)
	if err != nil || !passed {
		return false, "fail"
	}
	return true, "pass"
}

// Silence unused-import lint if scenario types shift away from orchestrator
// at compile time.
var _ = orchestrator.New
