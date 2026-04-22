package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/event-driven-tests-ai/edt/pkg/controlplane"
	"github.com/event-driven-tests-ai/edt/pkg/controlplane/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func contextBG() context.Context { return context.Background() }
func timeNowUTC() time.Time      { return time.Now().UTC() }

const minimalYAML = `apiVersion: edt.io/v1
kind: Scenario
metadata:
  name: demo
  labels:
    team: commerce
spec:
  connectors:
    kafka:
      bootstrap_servers: localhost:9092
  steps: []
`

func newTestServer(t *testing.T) (*httptest.Server, storage.Storage) {
	t.Helper()
	store := storage.NewMemStore()
	s := controlplane.NewServerWithStorage(controlplane.Config{}, store)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv, store
}

func TestPOSTCreatesScenario(t *testing.T) {
	srv, _ := newTestServer(t)

	resp, err := http.Post(srv.URL+"/api/v1/scenarios", "application/yaml", strings.NewReader(minimalYAML))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var got map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, "demo", got["name"])
	assert.EqualValues(t, 1, got["version"].(float64))
}

func TestPOSTAcceptsJSONWrapper(t *testing.T) {
	srv, _ := newTestServer(t)
	body, _ := json.Marshal(map[string]string{"yaml": minimalYAML})
	resp, err := http.Post(srv.URL+"/api/v1/scenarios", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
}

func TestPOSTRejectsInvalidYAML(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Post(srv.URL+"/api/v1/scenarios", "application/yaml", strings.NewReader("not: yaml: ["))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestPOSTRequiresMetadataName(t *testing.T) {
	srv, _ := newTestServer(t)
	body := `apiVersion: edt.io/v1
kind: Scenario
metadata: {}
spec:
  connectors: {}
  steps: []
`
	resp, err := http.Post(srv.URL+"/api/v1/scenarios", "application/yaml", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	body2 := readBody(t, resp)
	// Either the path-level handler check or the JSON Schema can fire first;
	// both report the missing name.
	assert.Contains(t, body2, "name")
}

func TestGETScenarioRoundTrip(t *testing.T) {
	srv, _ := newTestServer(t)
	_, err := http.Post(srv.URL+"/api/v1/scenarios", "application/yaml", strings.NewReader(minimalYAML))
	require.NoError(t, err)

	resp, err := http.Get(srv.URL + "/api/v1/scenarios/demo")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var got map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, "demo", got["name"])
	assert.Contains(t, got["yaml"].(string), "metadata:\n  name: demo")
}

func TestGETMissingReturns404(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/api/v1/scenarios/ghost")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestPUTBumpsVersion(t *testing.T) {
	srv, _ := newTestServer(t)
	_, err := http.Post(srv.URL+"/api/v1/scenarios", "application/yaml", strings.NewReader(minimalYAML))
	require.NoError(t, err)

	updated := strings.Replace(minimalYAML, "team: commerce", "team: payments", 1)
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/v1/scenarios/demo", strings.NewReader(updated))
	req.Header.Set("Content-Type", "application/yaml")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.EqualValues(t, 2, got["version"].(float64))
}

func TestListScenarios(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, n := range []string{"a", "b", "c"} {
		body := strings.Replace(minimalYAML, "name: demo", "name: "+n, 1)
		_, err := http.Post(srv.URL+"/api/v1/scenarios", "application/yaml", strings.NewReader(body))
		require.NoError(t, err)
	}
	resp, err := http.Get(srv.URL + "/api/v1/scenarios")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Len(t, got, 3)
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	var b bytes.Buffer
	_, _ = b.ReadFrom(resp.Body)
	return b.String()
}

func TestScenarioStateEndpointReturnsSamples(t *testing.T) {
	srv, store := newTestServer(t)

	// Seed a scenario + a watch-mode run whose checks become samples.
	ctx := contextBG()
	_, _ = store.UpsertScenario(ctx, "demo", []byte(minimalYAML), nil)
	_ = store.AppendCheckSamples(ctx, []storage.CheckSample{
		{Scenario: "demo", Check: "x", Ts: timeNowUTC().Add(-5 * time.Minute), Passed: true, Severity: "critical"},
		{Scenario: "demo", Check: "x", Ts: timeNowUTC(), Passed: false, Severity: "critical"},
		{Scenario: "other", Check: "x", Ts: timeNowUTC(), Passed: true, Severity: "warning"},
	})

	resp, err := http.Get(srv.URL + "/api/v1/scenarios/demo/state?window=10m")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	samples := got["samples"].([]any)
	assert.Len(t, samples, 2, "only the two demo samples within 10m must be returned")
}
