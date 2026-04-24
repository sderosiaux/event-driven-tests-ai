package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func postJSON(t *testing.T, url string, payload any) *http.Response {
	t.Helper()
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	return resp
}

func TestIngestEvalRunRoundTrip(t *testing.T) {
	srv, _ := newTestServer(t)

	now := time.Now().UTC().Truncate(time.Millisecond)
	payload := map[string]any{
		"id":          "eval-20260424T150000-abcd",
		"scenario":    "triage",
		"judge_model": "claude-opus-4-7",
		"iterations":  50,
		"started_at":  now.Add(-time.Minute),
		"finished_at": now,
		"status":      "pass",
		"results": []map[string]any{
			{
				"name":             "triage_correctness",
				"aggregate":        "avg",
				"samples":          50,
				"required_samples": 50,
				"value":            4.72,
				"threshold":        ">= 4.2",
				"passed":           true,
				"status":           "pass",
			},
			{
				"name":      "polite",
				"aggregate": "avg",
				"samples":   50,
				"value":     4.1,
				"threshold": ">= 4.2",
				"passed":    false,
				"status":    "fail",
			},
		},
	}

	resp := postJSON(t, srv.URL+"/api/v1/eval-runs", payload)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	get, err := http.Get(srv.URL + "/api/v1/eval-runs/eval-20260424T150000-abcd")
	require.NoError(t, err)
	defer get.Body.Close()
	require.Equal(t, http.StatusOK, get.StatusCode)

	var got map[string]any
	require.NoError(t, json.NewDecoder(get.Body).Decode(&got))
	run := got["run"].(map[string]any)
	assert.Equal(t, "triage", run["scenario"])
	assert.Equal(t, "claude-opus-4-7", run["judge_model"])
	assert.EqualValues(t, 50, run["iterations"])

	rows := got["results"].([]any)
	require.Len(t, rows, 2)
	byName := map[string]map[string]any{}
	for _, r := range rows {
		m := r.(map[string]any)
		byName[m["name"].(string)] = m
	}
	assert.Equal(t, "pass", byName["triage_correctness"]["status"])
	assert.Equal(t, false, byName["polite"]["passed"])
}

func TestIngestEvalRunRequiresIDAndScenario(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Post(srv.URL+"/api/v1/eval-runs", "application/json", strings.NewReader(`{"status":"pass"}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestListEvalRunsFiltersByScenario(t *testing.T) {
	srv, _ := newTestServer(t)
	now := time.Now().UTC()
	for i, s := range []string{"triage", "polite", "triage"} {
		_ = postJSON(t, srv.URL+"/api/v1/eval-runs", map[string]any{
			"id":          "eval-" + s + "-" + string(rune('0'+i)),
			"scenario":    s,
			"status":      "pass",
			"started_at":  now,
			"finished_at": now,
			"results":     []map[string]any{},
		}).Body.Close()
	}

	resp, err := http.Get(srv.URL + "/api/v1/eval-runs?scenario=triage")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Len(t, got, 2, "only triage runs should be returned")
	for _, r := range got {
		assert.Equal(t, "triage", r["scenario"])
	}
}

func TestGetEvalRunNotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/api/v1/eval-runs/missing")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}
