package checks

import (
	"time"

	"github.com/event-driven-tests-ai/edt/pkg/scenario"
)

// CheckResult is the outcome of evaluating a single Check against an event store.
type CheckResult struct {
	Name     string             `json:"name"`
	Expr     string             `json:"expr"`
	Severity scenario.Severity  `json:"severity"`
	Window   string             `json:"window,omitempty"`
	Passed   bool               `json:"passed"`
	Value    any                `json:"value,omitempty"` // observed evaluation value (when bool)
	Err      string             `json:"error,omitempty"`
	At       time.Time          `json:"at"`
}

// Failed is true when a check did not pass or returned an error.
func (r CheckResult) Failed() bool {
	return !r.Passed || r.Err != ""
}

// IsCritical returns true when the check both failed and is severity=critical.
// Used to drive the CLI exit code: a critical failure makes `edt run` exit 1.
func (r CheckResult) IsCritical() bool {
	return r.Failed() && r.Severity == scenario.SeverityCritical
}
