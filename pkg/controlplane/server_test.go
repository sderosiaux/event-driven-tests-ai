package controlplane_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/controlplane"
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

func TestUIIndexServesEmbeddedHTML(t *testing.T) {
	s := controlplane.NewServer(controlplane.Config{})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	for _, path := range []string{"/", "/ui/runs", "/ui/evals", "/ui/workers"} {
		resp, err := http.Get(srv.URL + path)
		require.NoError(t, err, path)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		assert.Equal(t, 200, resp.StatusCode, path)
		assert.Contains(t, string(body), `<title>edt — control plane</title>`, path)
	}
}

func TestUIStaticAssetsServed(t *testing.T) {
	s := controlplane.NewServer(controlplane.Config{})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ui/static/app.js")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "fetchJSON")
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

// M6-T6: the MCP write policy integrates with the HTTP auth middleware. An
// editor-role bearer must be permitted to call upsert_scenario; a viewer
// bearer must be rejected with the policy error code.
func TestMCPWritePolicyGatesOnHTTPRole(t *testing.T) {
	s := controlplane.NewServer(controlplane.Config{
		RequireAuth: true,
		AdminToken:  "admin-bootstrap-secret",
	})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	mintToken := func(role string) string {
		payload := `{"role":"` + role + `","note":"mcp-test"}`
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/tokens", strings.NewReader(payload))
		req.Header.Set("Authorization", "Bearer admin-bootstrap-secret")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, 201, resp.StatusCode)
		var out map[string]string
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
		return out["plaintext"]
	}
	editor := mintToken("editor")
	viewer := mintToken("viewer")

	scenarioYAML := "apiVersion: edt.io/v1\nkind: Scenario\nmetadata:\n  name: via-mcp\nspec:\n  connectors: {}\n  steps: []\n"
	mcpBody := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"upsert_scenario","arguments":{"name":"via-mcp","yaml":` +
		strconv.Quote(scenarioYAML) + `}}}`

	callMCP := func(tok string) map[string]any {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(mcpBody))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		var out map[string]any
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
		return out
	}

	viewerResp := callMCP(viewer)
	errObj, ok := viewerResp["error"].(map[string]any)
	require.True(t, ok, "viewer must be denied")
	assert.Equal(t, float64(-32604), errObj["code"], "policy-denied code expected")

	editorResp := callMCP(editor)
	assert.Nil(t, editorResp["error"], "editor write must succeed, got: %v", editorResp["error"])
}
