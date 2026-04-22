package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/event-driven-tests-ai/edt/pkg/controlplane/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpsertBumpsVersion(t *testing.T) {
	s := storage.NewMemStore()
	ctx := context.Background()
	v1, err := s.UpsertScenario(ctx, "demo", []byte("name: demo"), map[string]string{"team": "x"})
	require.NoError(t, err)
	assert.Equal(t, 1, v1.Version)

	v2, err := s.UpsertScenario(ctx, "demo", []byte("name: demo\nversion: 2"), nil)
	require.NoError(t, err)
	assert.Equal(t, 2, v2.Version)
	assert.True(t, v2.UpdatedAt.After(v1.UpdatedAt) || v2.UpdatedAt.Equal(v1.UpdatedAt))

	got, err := s.GetScenario(ctx, "demo")
	require.NoError(t, err)
	assert.Equal(t, 2, got.Version)
	assert.Contains(t, string(got.YAML), "version: 2")
}

func TestGetScenarioMissing(t *testing.T) {
	s := storage.NewMemStore()
	_, err := s.GetScenario(context.Background(), "ghost")
	assert.ErrorIs(t, err, storage.ErrNotFound)
}

func TestRecordAndGetRun(t *testing.T) {
	s := storage.NewMemStore()
	ctx := context.Background()
	r := storage.Run{
		ID: "r-1", Scenario: "demo", Mode: "run", Status: "pass", ExitCode: 0,
		StartedAt: time.Now(), FinishedAt: time.Now(),
	}
	checks := []storage.CheckResult{
		{Name: "ack_p99", Severity: "critical", Passed: true},
		{Name: "warn", Severity: "warning", Passed: false, Err: "boom"},
	}
	require.NoError(t, s.RecordRun(ctx, r, checks))

	got, gotChecks, err := s.GetRun(ctx, "r-1")
	require.NoError(t, err)
	assert.Equal(t, "demo", got.Scenario)
	require.Len(t, gotChecks, 2)
	assert.Equal(t, "r-1", gotChecks[0].RunID)
}

func TestListRunsScenarioFilterAndLimit(t *testing.T) {
	s := storage.NewMemStore()
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_ = s.RecordRun(ctx, storage.Run{
			ID: "r-" + string(rune('a'+i)), Scenario: "demo", StartedAt: time.Now().Add(time.Duration(i) * time.Minute),
		}, nil)
	}
	_ = s.RecordRun(ctx, storage.Run{ID: "r-other", Scenario: "other", StartedAt: time.Now()}, nil)

	got, err := s.ListRuns(ctx, "demo", 3)
	require.NoError(t, err)
	require.Len(t, got, 3)
	for _, r := range got {
		assert.Equal(t, "demo", r.Scenario)
	}

	all, err := s.ListRuns(ctx, "", 0)
	require.NoError(t, err)
	assert.Len(t, all, 6)
}

func TestSLOPassRateRespectsWindowAndScenario(t *testing.T) {
	s := storage.NewMemStore()
	ctx := context.Background()
	now := time.Now()
	older := now.Add(-2 * time.Hour) // outside the 1h window

	_ = s.RecordRun(ctx, storage.Run{ID: "r-1", Scenario: "demo", StartedAt: older}, []storage.CheckResult{
		{Name: "x", Passed: false}, // ignored (too old)
	})
	_ = s.RecordRun(ctx, storage.Run{ID: "r-2", Scenario: "demo", StartedAt: now}, []storage.CheckResult{
		{Name: "x", Passed: true},
		{Name: "y", Passed: false},
	})
	_ = s.RecordRun(ctx, storage.Run{ID: "r-3", Scenario: "demo", StartedAt: now}, []storage.CheckResult{
		{Name: "x", Passed: true},
		{Name: "y", Passed: true},
	})

	rates, err := s.SLOPassRate(ctx, "demo", time.Hour)
	require.NoError(t, err)
	assert.InDelta(t, 1.0, rates["x"], 1e-9, "x: 2/2 in window")
	assert.InDelta(t, 0.5, rates["y"], 1e-9, "y: 1/2 in window")
}

func TestWorkerLifecycle(t *testing.T) {
	s := storage.NewMemStore()
	ctx := context.Background()
	w, err := s.RegisterWorker(ctx, map[string]string{"env": "staging"}, "v0.1.0")
	require.NoError(t, err)
	assert.NotEmpty(t, w.ID)
	assert.Equal(t, "staging", w.Labels["env"])

	_, err = s.UpsertScenario(ctx, "demo", []byte("x"), nil)
	require.NoError(t, err)
	require.NoError(t, s.AssignScenario(ctx, w.ID, "demo"))

	as, err := s.ListAssignments(ctx, w.ID)
	require.NoError(t, err)
	require.Len(t, as, 1)
	assert.Equal(t, "demo", as[0].ScenarioName)

	require.NoError(t, s.Heartbeat(ctx, w.ID))
	assert.ErrorIs(t, s.Heartbeat(ctx, "ghost"), storage.ErrNotFound)
	assert.ErrorIs(t, s.AssignScenario(ctx, "ghost", "demo"), storage.ErrNotFound)
	assert.ErrorIs(t, s.AssignScenario(ctx, w.ID, "ghost"), storage.ErrNotFound)
}
