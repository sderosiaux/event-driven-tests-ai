package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/controlplane/mcp"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/controlplane/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMCP(t *testing.T) (*httptest.Server, storage.Storage) {
	t.Helper()
	s := storage.NewMemStore()
	srv := httptest.NewServer(mcp.New(s).Handler())
	t.Cleanup(srv.Close)
	return srv, s
}

func call(t *testing.T, url string, payload any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(payload)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

func TestInitializeReportsCapabilities(t *testing.T) {
	srv, _ := newMCP(t)
	got := call(t, srv.URL, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	assert.Equal(t, "2.0", got["jsonrpc"])
	result := got["result"].(map[string]any)
	assert.Equal(t, "2025-03-26", result["protocolVersion"])
	assert.NotNil(t, result["capabilities"].(map[string]any)["tools"])
}

func TestToolsListReturnsSixTools(t *testing.T) {
	srv, _ := newMCP(t)
	got := call(t, srv.URL, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/list",
	})
	result := got["result"].(map[string]any)
	tools := result["tools"].([]any)
	assert.Len(t, tools, 6)
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"list_scenarios", "get_scenario", "list_runs", "get_run", "scenario_slo", "list_workers"} {
		assert.True(t, names[want], "missing tool %s", want)
	}
}

func TestToolsCallListScenarios(t *testing.T) {
	srv, store := newMCP(t)
	_, err := store.UpsertScenario(context.Background(), "demo", []byte("name: demo"), nil)
	require.NoError(t, err)

	got := call(t, srv.URL, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "list_scenarios", "arguments": map[string]any{}},
	})
	result := got["result"].(map[string]any)
	content := result["content"].([]any)
	require.NotEmpty(t, content)
	// MCP wraps the tool output as a text block — decode the inner JSON.
	text := content[0].(map[string]any)["text"].(string)
	var scenarios []map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &scenarios))
	assert.Len(t, scenarios, 1)
	assert.Equal(t, "demo", scenarios[0]["Name"])
}

// Codex P1 #13: missing resources must surface -32002, not generic -32603.
func TestToolsCallGetScenarioReturnsNotFoundCode(t *testing.T) {
	srv, _ := newMCP(t)
	got := call(t, srv.URL, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]any{"name": "get_scenario", "arguments": map[string]any{"name": "ghost"}},
	})
	errObj := got["error"].(map[string]any)
	assert.Equal(t, float64(-32002), errObj["code"])
	assert.Contains(t, errObj["message"], "not found")
	data := errObj["data"].(map[string]any)
	assert.Equal(t, "get_scenario", data["tool"])
}

// Codex P1 #12: successful tool calls must advertise isError:false.
func TestToolsCallSuccessIncludesIsErrorFalse(t *testing.T) {
	srv, _ := newMCP(t)
	got := call(t, srv.URL, map[string]any{
		"jsonrpc": "2.0", "id": 20, "method": "tools/call",
		"params": map[string]any{"name": "list_workers", "arguments": map[string]any{}},
	})
	result := got["result"].(map[string]any)
	assert.Equal(t, false, result["isError"])
}

// Codex P1 #12: tool descriptors must carry annotations so clients can reason
// about safety without reading the description.
func TestToolsListCarriesReadOnlyAnnotations(t *testing.T) {
	srv, _ := newMCP(t)
	got := call(t, srv.URL, map[string]any{
		"jsonrpc": "2.0", "id": 21, "method": "tools/list",
	})
	tools := got["result"].(map[string]any)["tools"].([]any)
	for _, tool := range tools {
		annotations := tool.(map[string]any)["annotations"].(map[string]any)
		assert.Equal(t, true, annotations["readOnlyHint"], "tool=%v", tool.(map[string]any)["name"])
		assert.Equal(t, false, annotations["destructiveHint"])
	}
}

// Codex P1 #11: JSON-RPC batch support is required by MCP 2025-03-26.
func TestBatchRequestReturnsArrayOfResponses(t *testing.T) {
	srv, store := newMCP(t)
	_, _ = store.UpsertScenario(context.Background(), "demo", []byte("x"), nil)

	body, _ := json.Marshal([]map[string]any{
		{"jsonrpc": "2.0", "id": "a", "method": "initialize"},
		{"jsonrpc": "2.0", "id": "b", "method": "tools/list"},
	})
	resp, err := http.Post(srv.URL, "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	var out []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Len(t, out, 2)
	assert.Equal(t, "a", out[0]["id"])
	assert.Equal(t, "b", out[1]["id"])
}

// Codex P1 #11: notifications (no `id`) must not receive a JSON-RPC body.
func TestNotificationReturns202WithNoBody(t *testing.T) {
	srv, _ := newMCP(t)
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "method": "tools/list",
	})
	resp, err := http.Post(srv.URL, "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)
	raw, _ := io.ReadAll(resp.Body)
	assert.Empty(t, raw)
}

func TestUnknownMethodReturnsRPCError(t *testing.T) {
	srv, _ := newMCP(t)
	got := call(t, srv.URL, map[string]any{
		"jsonrpc": "2.0", "id": 5, "method": "tools/invoke",
	})
	errObj := got["error"].(map[string]any)
	assert.Equal(t, float64(-32601), errObj["code"])
}

func TestUnknownToolReturnsRPCError(t *testing.T) {
	srv, _ := newMCP(t)
	got := call(t, srv.URL, map[string]any{
		"jsonrpc": "2.0", "id": 6, "method": "tools/call",
		"params": map[string]any{"name": "bogus", "arguments": map[string]any{}},
	})
	errObj := got["error"].(map[string]any)
	assert.Equal(t, float64(-32603), errObj["code"])
	assert.Contains(t, errObj["message"], "unknown tool")
}

func TestGETMethodRejected(t *testing.T) {
	srv, _ := newMCP(t)
	resp, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer resp.Body.Close()
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	errObj := out["error"].(map[string]any)
	assert.Equal(t, float64(-32600), errObj["code"])
}
