package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/event-driven-tests-ai/edt/pkg/checks"
	"github.com/event-driven-tests-ai/edt/pkg/report"
	"github.com/event-driven-tests-ai/edt/pkg/scenario"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mkReport(scenarioName, runID string, results []checks.CheckResult) *report.Report {
	r := &report.Report{
		Scenario:  scenarioName,
		RunID:     runID,
		Mode:      "run",
		StartedAt: time.Now().UTC().Add(-time.Minute),
		Checks:    results,
	}
	r.Finalize()
	return r
}

func postRun(t *testing.T, baseURL string, r *report.Report) *http.Response {
	t.Helper()
	body, err := json.Marshal(r)
	require.NoError(t, err)
	resp, err := http.Post(baseURL+"/api/v1/runs", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	return resp
}

func TestIngestRunPersistsAndRoundTrips(t *testing.T) {
	srv, _ := newTestServer(t)
	r := mkReport("demo", "r-1", []checks.CheckResult{
		{Name: "ack_p99", Severity: scenario.SeverityCritical, Passed: true, Value: 12.5},
	})
	resp := postRun(t, srv.URL, r)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	get, err := http.Get(srv.URL + "/api/v1/runs/r-1")
	require.NoError(t, err)
	defer get.Body.Close()
	require.Equal(t, http.StatusOK, get.StatusCode)
	var got map[string]any
	require.NoError(t, json.NewDecoder(get.Body).Decode(&got))
	run := got["run"].(map[string]any)
	assert.Equal(t, "demo", run["scenario"])
	checks := got["checks"].([]any)
	require.Len(t, checks, 1)
	assert.Equal(t, "ack_p99", checks[0].(map[string]any)["name"])
}

func TestIngestRunRequiresScenarioAndRunID(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Post(srv.URL+"/api/v1/runs", "application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestListRunsScenarioFilterAndLimit(t *testing.T) {
	srv, _ := newTestServer(t)
	for i, name := range []string{"a", "b", "a"} {
		r := mkReport(name, "r-"+name+string(rune('0'+i)), nil)
		resp := postRun(t, srv.URL, r)
		_ = resp.Body.Close()
	}

	get, err := http.Get(srv.URL + "/api/v1/runs?scenario=a")
	require.NoError(t, err)
	defer get.Body.Close()
	var got []map[string]any
	require.NoError(t, json.NewDecoder(get.Body).Decode(&got))
	assert.Len(t, got, 2)
	for _, run := range got {
		assert.Equal(t, "a", run["scenario"])
	}
}

func TestSLOPassRateAggregation(t *testing.T) {
	srv, _ := newTestServer(t)
	postRun(t, srv.URL, mkReport("demo", "r-1", []checks.CheckResult{
		{Name: "x", Severity: scenario.SeverityCritical, Passed: true},
		{Name: "y", Severity: scenario.SeverityWarning, Passed: false},
	})).Body.Close()
	postRun(t, srv.URL, mkReport("demo", "r-2", []checks.CheckResult{
		{Name: "x", Severity: scenario.SeverityCritical, Passed: true},
		{Name: "y", Severity: scenario.SeverityWarning, Passed: true},
	})).Body.Close()

	resp, err := http.Get(srv.URL + "/api/v1/scenarios/demo/slo?window=1h")
	require.NoError(t, err)
	defer resp.Body.Close()
	var got map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, "demo", got["scenario"])
	rates := got["pass_rates"].(map[string]any)
	assert.InDelta(t, 1.0, rates["x"].(float64), 1e-9)
	assert.InDelta(t, 0.5, rates["y"].(float64), 1e-9)
}

func TestGetRunMissing(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/api/v1/runs/ghost")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}
