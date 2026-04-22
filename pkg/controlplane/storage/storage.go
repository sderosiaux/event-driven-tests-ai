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

	// SLO
	SLOPassRate(ctx context.Context, scenario string, window time.Duration) (map[string]float64, error)

	// Workers
	RegisterWorker(ctx context.Context, labels map[string]string, version string) (Worker, error)
	Heartbeat(ctx context.Context, id string) error
	ListWorkers(ctx context.Context) ([]Worker, error)
	AssignScenario(ctx context.Context, workerID, scenarioName string) error
	ListAssignments(ctx context.Context, workerID string) ([]Assignment, error)

	Close() error
}
