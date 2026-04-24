// Package eval implements the agent-under-test harness: seed inputs, observe
// the agent's outputs, correlate input/output pairs by key, score each pair
// through a Judge (deterministic CEL + LLM-as-judge), and aggregate against a
// threshold.
//
// Judges are pluggable: unit tests use a StubJudge; production runs use the
// Anthropic-backed LLMJudge in anthropic.go.
package eval

import (
	"context"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
)

// Pair is an observed (input, output) pair the executor hands to the judge.
// Meta carries run-level context (scenario name, run id, iteration index) so
// the judge can thread it into prompt templates.
type Pair struct {
	Input  map[string]any
	Output map[string]any
	Meta   map[string]any
}

// Score is one judge verdict. Value is in [1, N] where N is the rubric
// top-of-scale (typically 5). Rationale is whatever the judge returned; for
// LLM judges this is the natural-language justification.
type Score struct {
	Value     float64
	Rationale string
	Err       error // non-nil → skipped from the aggregate
}

// Judge evaluates one pair against the Eval definition.
type Judge interface {
	Score(ctx context.Context, eval scenario.Eval, p Pair) (Score, error)
}
