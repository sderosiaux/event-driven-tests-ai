package worker_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/controlplane"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/controlplane/storage"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Codex resume path: a fresh worker process must be able to fetch prior
// check samples persisted by the control plane so its windowed state starts
// warm instead of cold.
func TestFetchScenarioStateReturnsSeededSamples(t *testing.T) {
	store := storage.NewMemStore()
	cp := controlplane.NewServerWithStorage(controlplane.Config{}, store)
	srv := httptest.NewServer(cp.Handler())
	defer srv.Close()

	ctx := context.Background()
	_, err := store.UpsertScenario(ctx, "demo", []byte("name: demo"), nil)
	require.NoError(t, err)

	now := time.Now().UTC()
	require.NoError(t, store.AppendCheckSamples(ctx, []storage.CheckSample{
		{Scenario: "demo", Check: "x", Ts: now.Add(-90 * time.Minute), Passed: true, Severity: "warning"}, // stale, window filters
		{Scenario: "demo", Check: "x", Ts: now.Add(-2 * time.Minute), Passed: true, Severity: "critical"},
		{Scenario: "demo", Check: "x", Ts: now, Passed: false, Severity: "critical"},
	}))

	c := worker.NewClient(srv.URL, "")
	samples, err := c.FetchScenarioState(ctx, "demo", 10*time.Minute)
	require.NoError(t, err)
	assert.Len(t, samples, 2, "only the two samples within the 10m window should come back")
	// Ordering: server returns ascending Ts.
	assert.False(t, samples[0].Passed != true) // earlier = pass
	assert.False(t, samples[1].Passed)          // later  = fail
}
