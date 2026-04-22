package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func registerWorker(t *testing.T, baseURL string) string {
	t.Helper()
	resp, err := http.Post(baseURL+"/api/v1/workers/register", "application/json",
		strings.NewReader(`{"labels":{"env":"staging"},"version":"v0.1.0"}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var got map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.NotEmpty(t, got["worker_id"])
	return got["worker_id"]
}

func TestRegisterAndListWorkers(t *testing.T) {
	srv, _ := newTestServer(t)
	id := registerWorker(t, srv.URL)

	resp, err := http.Get(srv.URL + "/api/v1/workers")
	require.NoError(t, err)
	defer resp.Body.Close()
	var got []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Len(t, got, 1)
	assert.Equal(t, id, got[0]["id"])
	labels := got[0]["labels"].(map[string]any)
	assert.Equal(t, "staging", labels["env"])
}

func TestHeartbeatReturnsAssignments(t *testing.T) {
	srv, _ := newTestServer(t)
	id := registerWorker(t, srv.URL)

	// Need a scenario to assign.
	_, err := http.Post(srv.URL+"/api/v1/scenarios", "application/yaml", strings.NewReader(minimalYAML))
	require.NoError(t, err)
	body, _ := json.Marshal(map[string]string{"scenario": "demo"})
	resp, err := http.Post(srv.URL+"/api/v1/workers/"+id+"/assignments", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	resp.Body.Close()

	hb, err := http.Post(srv.URL+"/api/v1/workers/"+id+"/heartbeat", "application/json", http.NoBody)
	require.NoError(t, err)
	defer hb.Body.Close()
	require.Equal(t, http.StatusOK, hb.StatusCode)
	var got map[string]any
	require.NoError(t, json.NewDecoder(hb.Body).Decode(&got))
	assert.Equal(t, id, got["worker_id"])
	as := got["assignments"].([]any)
	require.Len(t, as, 1)
	assert.Equal(t, "demo", as[0].(map[string]any)["scenario"])
}

func TestHeartbeatUnknownWorker404(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Post(srv.URL+"/api/v1/workers/ghost/heartbeat", "application/json", http.NoBody)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestAssignScenarioRejectsEmpty(t *testing.T) {
	srv, _ := newTestServer(t)
	id := registerWorker(t, srv.URL)
	resp, err := http.Post(srv.URL+"/api/v1/workers/"+id+"/assignments", "application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}
