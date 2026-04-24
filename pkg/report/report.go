// Package report turns a run's results into machine- and human-readable output.
//
// The JSON form is the contract shipped to the control plane. The console form
// is a terse summary tuned for CI logs and local development.
package report

import (
	"time"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/checks"
)

// Status is the top-level run outcome.
type Status string

const (
	StatusPass  Status = "pass"
	StatusFail  Status = "fail"
	StatusError Status = "error"
)

// Report describes a single run.
type Report struct {
	Scenario   string               `json:"scenario"`
	RunID      string               `json:"run_id"`
	Mode       string               `json:"mode"` // "run" | "watch" | "eval"
	StartedAt  time.Time            `json:"started_at"`
	FinishedAt time.Time            `json:"finished_at"`
	Duration   time.Duration        `json:"duration_ns"`
	Status     Status               `json:"status"`
	ExitCode   int                  `json:"exit_code"`
	Checks     []checks.CheckResult `json:"checks"`
	EventCount int                  `json:"event_count"`
	Error      string               `json:"error,omitempty"`
}

// Finalize fills status + exit code from the check results.
// Returns the receiver so call sites can chain.
func (r *Report) Finalize() *Report {
	r.FinishedAt = time.Now().UTC()
	r.Duration = r.FinishedAt.Sub(r.StartedAt)
	if r.Error != "" {
		r.Status = StatusError
		r.ExitCode = 2
		return r
	}
	if checks.AnyCriticalFailed(r.Checks) {
		r.Status = StatusFail
		r.ExitCode = 1
		return r
	}
	r.Status = StatusPass
	r.ExitCode = 0
	return r
}
