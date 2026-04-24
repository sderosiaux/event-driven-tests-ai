//go:build integration

// Postgres-backed Storage tests. Require Docker.
// Run with: go test -tags=integration ./pkg/controlplane/storage/...
package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/controlplane/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func startPG(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	c, err := tcpg.Run(ctx, "postgres:16-alpine",
		tcpg.WithDatabase("edt_test"),
		tcpg.WithUsername("edt"),
		tcpg.WithPassword("edt"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	return dsn
}

func openPG(t *testing.T) storage.Storage {
	dsn := startPG(t)
	pg, err := storage.NewPGStore(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Close() })
	return pg
}

func TestPGRunsThroughTheFullScenarioLifecycle(t *testing.T) {
	s := openPG(t)
	ctx := context.Background()

	v1, err := s.UpsertScenario(ctx, "demo", []byte("name: demo"), map[string]string{"team": "x"})
	require.NoError(t, err)
	assert.Equal(t, 1, v1.Version)

	v2, err := s.UpsertScenario(ctx, "demo", []byte("name: demo\nv: 2"), nil)
	require.NoError(t, err)
	assert.Equal(t, 2, v2.Version)

	got, err := s.GetScenario(ctx, "demo")
	require.NoError(t, err)
	assert.Equal(t, 2, got.Version)
	assert.Contains(t, string(got.YAML), "v: 2")
}

func TestPGRecordRunAndChecksRoundTrip(t *testing.T) {
	s := openPG(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	r := storage.Run{
		ID: "r-pg-1", Scenario: "demo", Mode: "run", Status: "pass",
		StartedAt: now, FinishedAt: now.Add(2 * time.Second), Duration: 2 * time.Second,
		Report: []byte(`{"k":1}`),
	}
	checks := []storage.CheckResult{
		{Name: "p99", Severity: "critical", Window: "5m", Passed: true, Value: "12.5", At: now},
		{Name: "warn", Severity: "warning", Passed: false, Err: "boom", At: now},
	}
	require.NoError(t, s.RecordRun(ctx, r, checks))

	got, gotChecks, err := s.GetRun(ctx, "r-pg-1")
	require.NoError(t, err)
	assert.Equal(t, "demo", got.Scenario)
	assert.Equal(t, 2*time.Second, got.Duration)
	require.Len(t, gotChecks, 2)
	assert.Equal(t, "5m", gotChecks[0].Window)
	assert.Equal(t, "boom", gotChecks[1].Err)
}

func TestPGSLOPassRate(t *testing.T) {
	s := openPG(t)
	ctx := context.Background()
	now := time.Now().UTC()

	for i, status := range []bool{true, true, false, true} {
		runID := "r-slo-" + string(rune('a'+i))
		require.NoError(t, s.RecordRun(ctx, storage.Run{
			ID: runID, Scenario: "demo", Status: "pass",
			StartedAt: now, FinishedAt: now,
		}, []storage.CheckResult{{Name: "x", Passed: status, At: now}}))
	}

	rates, err := s.SLOPassRate(ctx, "demo", time.Hour)
	require.NoError(t, err)
	assert.InDelta(t, 0.75, rates["x"], 1e-9)
}

func TestPGWorkerLifecycleIntegratesAssignments(t *testing.T) {
	s := openPG(t)
	ctx := context.Background()

	w, err := s.RegisterWorker(ctx, map[string]string{"env": "ci"}, "v0.0.1")
	require.NoError(t, err)
	require.NoError(t, s.Heartbeat(ctx, w.ID))

	_, err = s.UpsertScenario(ctx, "demo", []byte("x"), nil)
	require.NoError(t, err)
	require.NoError(t, s.AssignScenario(ctx, w.ID, "demo"))

	as, err := s.ListAssignments(ctx, w.ID)
	require.NoError(t, err)
	require.Len(t, as, 1)
	assert.Equal(t, "demo", as[0].ScenarioName)

	assert.ErrorIs(t, s.AssignScenario(ctx, "ghost", "demo"), storage.ErrNotFound)
	assert.ErrorIs(t, s.AssignScenario(ctx, w.ID, "ghost"), storage.ErrNotFound)
}

func TestPGTokenLifecycle(t *testing.T) {
	s := openPG(t)
	ctx := context.Background()

	t1, err := s.IssueToken(ctx, storage.RoleAdmin, "bootstrap")
	require.NoError(t, err)
	require.NotEmpty(t, t1.Plaintext)
	require.NotEmpty(t, t1.ID)

	got, err := s.LookupToken(ctx, t1.Plaintext)
	require.NoError(t, err)
	assert.Equal(t, storage.RoleAdmin, got.Role)
	assert.Empty(t, got.Plaintext, "Lookup must not re-emit the plaintext")

	_, err = s.LookupToken(ctx, "ghost")
	assert.ErrorIs(t, err, storage.ErrNotFound)

	// IssueTokenWithPlaintext must be idempotent on the bootstrap path.
	t2, err := s.IssueTokenWithPlaintext(ctx, t1.Plaintext, storage.RoleAdmin, "bootstrap")
	require.NoError(t, err)
	assert.Equal(t, t1.ID, t2.ID)

	require.NoError(t, s.RevokeToken(ctx, t1.ID))
	_, err = s.LookupToken(ctx, t1.Plaintext)
	assert.ErrorIs(t, err, storage.ErrNotFound)
}

func TestPGMigrationsAreIdempotent(t *testing.T) {
	dsn := startPG(t)
	for i := 0; i < 3; i++ {
		pg, err := storage.NewPGStore(context.Background(), dsn)
		require.NoError(t, err, "iteration %d", i)
		require.NoError(t, pg.Close())
	}
}
