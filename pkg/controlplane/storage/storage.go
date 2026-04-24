// Package storage backs the control plane. M2 ships an in-memory
// implementation (used in tests + dev mode) and a Postgres implementation
// (used in production). Both satisfy the same Storage interface.
package storage

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned when a lookup misses.
var ErrNotFound = errors.New("storage: not found")

// Scenario is the persisted shape of a versioned scenario YAML document.
type Scenario struct {
	Name      string
	Version   int       // monotonically increasing per name
	YAML      []byte    // canonical YAML body
	Labels    map[string]string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CheckSample is a single check observation persisted for worker-resume replay.
// It is the minimum shape needed to rehydrate a watch-mode worker's windowed
// check state after a restart.
type CheckSample struct {
	Scenario string
	Check    string
	Ts       time.Time
	Passed   bool
	Value    string // JSON-encoded raw value, same shape as CheckResult.Value
	Severity string
	Window   string
}

// Run mirrors pkg/report.Report fields the control plane needs to query on.
// The full report JSON is kept verbatim so future fields are preserved without
// schema migrations.
type Run struct {
	ID         string
	Scenario   string
	Mode       string
	Status     string // pass | fail | error
	ExitCode   int
	StartedAt  time.Time
	FinishedAt time.Time
	Duration   time.Duration
	Report     []byte // JSON-encoded report.Report
}

// EvalRun is the persisted shape of one `edt eval` invocation.
type EvalRun struct {
	ID         string
	Scenario   string
	JudgeModel string
	Iterations int
	StartedAt  time.Time
	FinishedAt time.Time
	Status     string // pass | fail | error
}

// EvalResult is one row of the eval_results fan-out — one per eval name.
type EvalResult struct {
	RunID           string
	Name            string
	JudgeModel      string // per-eval judge model; run-level model is the rollup
	Aggregate       string
	Samples         int
	RequiredSamples int
	Value           float64
	Threshold       string
	Passed          bool
	Status          string // pass | fail | insufficient_samples | judge_errors
	Errors          int
}

// CheckResult is the per-check row used to power SLO queries.
type CheckResult struct {
	RunID    string
	Name     string
	Severity string
	Window   string
	Passed   bool
	Value    string // JSON-encoded raw value (number, bool, ...)
	Err      string
	At       time.Time
}

// Role enumerates token capabilities.
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleEditor Role = "editor"
	RoleViewer Role = "viewer"
	RoleWorker Role = "worker"
)

// Token is a bearer token held by a user or worker.
type Token struct {
	ID        string
	Plaintext string // present only when freshly minted; never re-emitted
	Role      Role
	Note      string
	CreatedAt time.Time
}

// Worker is a registered worker capable of running scenarios.
type Worker struct {
	ID            string
	Labels        map[string]string
	Version       string
	RegisteredAt  time.Time
	LastHeartbeat time.Time
}

// Assignment maps a scenario to a worker for watch-mode execution.
type Assignment struct {
	WorkerID     string
	ScenarioName string
	AssignedAt   time.Time
}

// Storage is the control-plane persistence contract.
//
// Implementations must be safe for concurrent use. Read methods return a copy
// of the data so callers can mutate freely.
type Storage interface {
	// Scenarios
	UpsertScenario(ctx context.Context, name string, yaml []byte, labels map[string]string) (Scenario, error)
	GetScenario(ctx context.Context, name string) (Scenario, error)
	ListScenarios(ctx context.Context) ([]Scenario, error)

	// Runs
	RecordRun(ctx context.Context, r Run, checks []CheckResult) error
	GetRun(ctx context.Context, id string) (Run, []CheckResult, error)
	ListRuns(ctx context.Context, scenario string, limit int) ([]Run, error)

	// Eval runs
	RecordEvalRun(ctx context.Context, r EvalRun, results []EvalResult) error
	GetEvalRun(ctx context.Context, id string) (EvalRun, []EvalResult, error)
	ListEvalRuns(ctx context.Context, scenario string, limit int) ([]EvalRun, error)

	// SLO
	SLOPassRate(ctx context.Context, scenario string, window time.Duration) (map[string]float64, error)

	// Samples (watch-mode resume)
	AppendCheckSamples(ctx context.Context, samples []CheckSample) error
	LoadCheckSamplesSince(ctx context.Context, scenario string, since time.Time) ([]CheckSample, error)

	// Workers
	RegisterWorker(ctx context.Context, labels map[string]string, version string) (Worker, error)
	Heartbeat(ctx context.Context, id string) error
	ListWorkers(ctx context.Context) ([]Worker, error)
	AssignScenario(ctx context.Context, workerID, scenarioName string) error
	ListAssignments(ctx context.Context, workerID string) ([]Assignment, error)

	// Tokens
	IssueToken(ctx context.Context, role Role, note string) (Token, error)
	IssueTokenWithPlaintext(ctx context.Context, plaintext string, role Role, note string) (Token, error)
	LookupToken(ctx context.Context, plaintext string) (Token, error)
	ListTokens(ctx context.Context) ([]Token, error)
	RevokeToken(ctx context.Context, id string) error

	Close() error
}
