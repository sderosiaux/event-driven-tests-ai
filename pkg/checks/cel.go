// Package checks implements the CEL-based expression engine used to evaluate
// scenario checks. On top of standard CEL (arithmetic, comparisons, duration,
// timestamp), it registers streaming operators bound to an events.Store:
// stream, percentile, rate, latency, forall, exists, before.
package checks

import (
	"fmt"

	"github.com/event-driven-tests-ai/edt/pkg/events"
	"github.com/google/cel-go/cel"
)

// Evaluator compiles and runs CEL expressions against an events.Store.
//
// One Evaluator should be reused across multiple Evaluate calls for the same
// scenario; it holds a compiled CEL environment.
type Evaluator struct {
	env   *cel.Env
	store events.Store
}

// NewEvaluator builds an Evaluator wired to the given store.
// The store is captured by closure into store-bound functions (stream, latency).
func NewEvaluator(store events.Store) (*Evaluator, error) {
	e := &Evaluator{store: store}
	opts := append([]cel.EnvOption{cel.DefaultUTCTimeZone(true)}, storeFunctions(store)...)
	env, err := cel.NewEnv(opts...)
	if err != nil {
		return nil, fmt.Errorf("checks: build CEL env: %w", err)
	}
	e.env = env
	return e, nil
}

// Evaluate compiles and evaluates a CEL expression, returning the raw Go value.
// Empty inputs such as `nil` slices decode to their Go zero equivalent.
func (e *Evaluator) Evaluate(expr string) (any, error) {
	ast, issues := e.env.Compile(expr)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("checks: compile %q: %w", expr, issues.Err())
	}
	prg, err := e.env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("checks: program %q: %w", expr, err)
	}
	out, _, err := prg.Eval(map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("checks: eval %q: %w", expr, err)
	}
	return out.Value(), nil
}

// EvaluateBool compiles and evaluates an expression expected to return bool.
// Non-bool results return an error so callers can fail fast on malformed checks.
func (e *Evaluator) EvaluateBool(expr string) (bool, error) {
	v, err := e.Evaluate(expr)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("checks: expected bool result, got %T", v)
	}
	return b, nil
}
