package controlplane_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/event-driven-tests-ai/edt/pkg/controlplane"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthzReturnsOK(t *testing.T) {
	s := controlplane.NewServer(controlplane.Config{})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "ok", body["status"])
}

func TestRunStopsOnContextCancel(t *testing.T) {
	s := controlplane.NewServer(controlplane.Config{Addr: "127.0.0.1:0"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}

func TestMetricsEndpointReflectsIngestedRuns(t *testing.T) {
	s := controlplane.NewServer(controlplane.Config{})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	body := `{"scenario":"demo","run_id":"r-m-1","mode":"run","status":"pass","exit_code":0,"started_at":"2026-04-22T12:00:00Z","finished_at":"2026-04-22T12:00:01Z","duration_ns":1000000000,"checks":[{"name":"x","severity":"critical","passed":true}]}`
	resp, err := http.Post(srv.URL+"/api/v1/runs", "application/json",
		http.NoBody)
	_ = resp
	_ = err
	// Re-do with body since http.NoBody above was a placeholder mistake.
	resp, err = http.Post(srv.URL+"/api/v1/runs", "application/json",
		strings.NewReader(body))
	require.NoError(t, err)
	resp.Body.Close()

	mresp, err := http.Get(srv.URL + "/metrics")
	require.NoError(t, err)
	defer mresp.Body.Close()
	assert.Equal(t, 200, mresp.StatusCode)
	out, _ := io.ReadAll(mresp.Body)
	assert.Contains(t, string(out), `edt_run_total`)
	assert.Contains(t, string(out), `scenario="demo"`)
}

func TestUnknownRouteReturns404(t *testing.T) {
	s := controlplane.NewServer(controlplane.Config{})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/no-such-thing")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 404, resp.StatusCode)
}
