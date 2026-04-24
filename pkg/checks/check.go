package checks

import (
	"time"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
)

// EvaluateAll runs every check in the list against the evaluator and
// returns a CheckResult per check, in declaration order.
//
// In `run` mode the Window field is informational only; checks see the entire
// event store. In `watch` mode the orchestrator is expected to slice the store
// into a windowed view before calling EvaluateAll.
//
// A check whose expression compiles but does not return a bool is reported as
// a failure with an error string; the rest of the list still runs.
func EvaluateAll(e *Evaluator, checks []scenario.Check) []CheckResult {
	out := make([]CheckResult, 0, len(checks))
	for _, c := range checks {
		out = append(out, evaluateOne(e, c))
	}
	return out
}

func evaluateOne(e *Evaluator, c scenario.Check) CheckResult {
	r := CheckResult{
		Name:     c.Name,
		Expr:     c.Expr,
		Severity: c.Severity,
		Window:   c.Window,
		At:       time.Now().UTC(),
	}
	if r.Severity == "" {
		r.Severity = scenario.SeverityWarning
	}

	v, err := e.Evaluate(c.Expr)
	if err != nil {
		r.Err = err.Error()
		return r
	}
	r.Value = v
	b, ok := v.(bool)
	if !ok {
		r.Err = "check expression must return bool"
		return r
	}
	r.Passed = b
	return r
}

// AnyCriticalFailed returns true if any of the results is a critical failure.
// Convenience for setting an exit code.
func AnyCriticalFailed(results []CheckResult) bool {
	for _, r := range results {
		if r.IsCritical() {
			return true
		}
	}
	return false
}
