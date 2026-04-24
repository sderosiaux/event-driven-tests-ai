// Package mcp implements a minimal Model Context Protocol server that exposes
// the control plane to LLM agents. The wire surface is JSON-RPC 2.0 over HTTP
// — `POST /mcp` accepts a single request object, returns a single response.
//
// We support the three methods agents actually need at M5:
//
//   - initialize                → capability handshake
//   - tools/list                → describe the tools below
//   - tools/call (name, args)   → execute one tool against the Storage
//
// Tools (mapped to Storage methods):
//
//   - list_scenarios()                        → []{name, version, labels}
//   - get_scenario(name)                      → {name, version, yaml, labels}
//   - list_runs(scenario?, limit?)            → last N runs
//   - get_run(run_id)                         → run + per-check results
//   - scenario_slo(name, window?)             → pass-rate per check
//   - list_workers()                          → registered workers + liveness
//
// Writes are intentionally absent from M5: a misbehaving agent should not be
// able to mint tokens, upsert scenarios, or delete runs. Write tools land in
// a follow-up once a permission policy is in place.
package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/event-driven-tests-ai/edt/pkg/controlplane/storage"
)

const jsonRPCVersion = "2.0"

// rpcRequest is the JSON-RPC 2.0 envelope for method calls.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcResponse is the JSON-RPC 2.0 envelope for replies.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Server wraps a Storage and exposes MCP-compliant JSON-RPC over HTTP.
type Server struct {
	store storage.Storage
}

func New(store storage.Storage) *Server { return &Server{store: store} }

// Handler returns the HTTP handler to mount at /mcp. Accepts either a single
// JSON-RPC request object or a batch array (MCP 2025-03-26 transport). Requests
// without `id` are treated as notifications: processed but no JSON-RPC body is
// returned (HTTP 202).
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeRPC(w, rpcResponse{JSONRPC: jsonRPCVersion, Error: &rpcError{Code: -32600, Message: "mcp: POST only"}})
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			writeRPC(w, rpcResponse{JSONRPC: jsonRPCVersion, Error: &rpcError{Code: -32700, Message: err.Error()}})
			return
		}
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 {
			writeRPC(w, rpcResponse{JSONRPC: jsonRPCVersion, Error: &rpcError{Code: -32700, Message: "empty request body"}})
			return
		}

		// Batch: [ {...}, {...} ].
		if trimmed[0] == '[' {
			var batch []rpcRequest
			if err := json.Unmarshal(trimmed, &batch); err != nil {
				writeRPC(w, rpcResponse{JSONRPC: jsonRPCVersion, Error: &rpcError{Code: -32700, Message: "batch parse error: " + err.Error()}})
				return
			}
			if len(batch) == 0 {
				writeRPC(w, rpcResponse{JSONRPC: jsonRPCVersion, Error: &rpcError{Code: -32600, Message: "empty batch"}})
				return
			}
			var out []rpcResponse
			for _, req := range batch {
				if isNotification(req) {
					_ = s.dispatch(r, req) // side-effect only
					continue
				}
				out = append(out, s.dispatch(r, req))
			}
			if len(out) == 0 {
				// All notifications — per spec the server MAY return 202 with no body.
				w.WriteHeader(http.StatusAccepted)
				return
			}
			writeRPCBatch(w, out)
			return
		}

		// Single request or notification.
		var req rpcRequest
		if err := json.Unmarshal(trimmed, &req); err != nil {
			writeRPC(w, rpcResponse{JSONRPC: jsonRPCVersion, Error: &rpcError{Code: -32700, Message: "parse error: " + err.Error()}})
			return
		}
		if isNotification(req) {
			_ = s.dispatch(r, req)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeRPC(w, s.dispatch(r, req))
	})
}

// isNotification reports whether the JSON-RPC request omitted `id` entirely.
// Per JSON-RPC 2.0 notifications must not receive a response.
func isNotification(req rpcRequest) bool {
	if len(req.ID) == 0 {
		return true
	}
	return bytes.Equal(bytes.TrimSpace(req.ID), []byte("null")) || bytes.TrimSpace(req.ID)[0] == 0
}

func writeRPCBatch(w http.ResponseWriter, responses []rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	for i := range responses {
		responses[i].JSONRPC = jsonRPCVersion
	}
	_ = json.NewEncoder(w).Encode(responses)
}

func writeRPC(w http.ResponseWriter, resp rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	resp.JSONRPC = jsonRPCVersion
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) dispatch(r *http.Request, req rpcRequest) rpcResponse {
	switch req.Method {
	case "initialize":
		return rpcResponse{
			ID: req.ID,
			Result: map[string]any{
				"protocolVersion": "2025-03-26",
				"serverInfo":      map[string]any{"name": "edt-controlplane", "version": "m5"},
				"capabilities":    map[string]any{"tools": map[string]any{}},
			},
		}
	case "tools/list":
		return rpcResponse{ID: req.ID, Result: map[string]any{"tools": toolDescriptors()}}
	case "tools/call":
		return s.toolsCall(r, req)
	default:
		return rpcResponse{ID: req.ID, Error: &rpcError{Code: -32601, Message: "method not found: " + req.Method}}
	}
}

// toolDescriptors is the static manifest clients discover through tools/list.
// Every tool carries the 2025-03-26 annotations so MCP clients (Claude Code,
// Cursor, Desktop) can reason about safety: readOnlyHint=true marks us as
// non-mutating; destructiveHint=false rules out side effects.
func toolDescriptors() []map[string]any {
	readOnly := map[string]any{
		"readOnlyHint":    true,
		"destructiveHint": false,
		"idempotentHint":  true,
		"openWorldHint":   false,
	}
	return []map[string]any{
		{
			"name":        "list_scenarios",
			"description": "List every scenario stored in the control plane.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
			"annotations": readOnly,
		},
		{
			"name":        "get_scenario",
			"description": "Fetch a scenario by name (returns YAML body + metadata).",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"name"},
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
				},
			},
			"annotations": readOnly,
		},
		{
			"name":        "list_runs",
			"description": "List the most recent runs, optionally filtered by scenario.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scenario": map[string]any{"type": "string"},
					"limit":    map[string]any{"type": "integer", "minimum": 1, "maximum": 1000},
				},
			},
			"annotations": readOnly,
		},
		{
			"name":        "get_run",
			"description": "Fetch a single run by id, including per-check results.",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"run_id"},
				"properties": map[string]any{
					"run_id": map[string]any{"type": "string"},
				},
			},
			"annotations": readOnly,
		},
		{
			"name":        "scenario_slo",
			"description": "Pass-rate per check for a scenario over a sliding window (default 1h).",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"name"},
				"properties": map[string]any{
					"name":   map[string]any{"type": "string"},
					"window": map[string]any{"type": "string", "description": "Go duration (e.g. '5m', '1h', '24h')"},
				},
			},
			"annotations": readOnly,
		},
		{
			"name":        "list_workers",
			"description": "List registered workers with last-heartbeat timestamps.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
			"annotations": readOnly,
		},
	}
}

type toolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *Server) toolsCall(r *http.Request, req rpcRequest) rpcResponse {
	var p toolsCallParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return rpcResponse{ID: req.ID, Error: &rpcError{Code: -32602, Message: "invalid params: " + err.Error()}}
	}
	out, err := s.invoke(r, p)
	if err != nil {
		// Missing resources get the MCP-conventional -32002 with the tool name
		// and arguments echoed in `data` so clients can present a helpful
		// message without a second probe.
		if errors.Is(err, storage.ErrNotFound) {
			return rpcResponse{ID: req.ID, Error: &rpcError{
				Code:    -32002,
				Message: err.Error(),
				Data:    map[string]any{"tool": p.Name, "arguments": json.RawMessage(p.Arguments)},
			}}
		}
		// Anything else is a genuine internal failure.
		return rpcResponse{ID: req.ID, Error: &rpcError{Code: -32603, Message: err.Error()}}
	}
	// MCP tool results wrap the payload in a content array and advertise
	// `isError: false` so a 2025-03-26 client can distinguish success from
	// tool-level failure without inspecting the payload.
	jsonBytes, _ := json.MarshalIndent(out, "", "  ")
	return rpcResponse{ID: req.ID, Result: map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(jsonBytes)}},
		"isError": false,
	}}
}

func (s *Server) invoke(r *http.Request, p toolsCallParams) (any, error) {
	ctx := r.Context()
	switch p.Name {
	case "list_scenarios":
		return s.store.ListScenarios(ctx)
	case "get_scenario":
		var args struct{ Name string }
		_ = json.Unmarshal(p.Arguments, &args)
		return s.store.GetScenario(ctx, args.Name)
	case "list_runs":
		var args struct {
			Scenario string
			Limit    int
		}
		_ = json.Unmarshal(p.Arguments, &args)
		if args.Limit <= 0 || args.Limit > 1000 {
			args.Limit = 50
		}
		return s.store.ListRuns(ctx, args.Scenario, args.Limit)
	case "get_run":
		var args struct{ RunID string }
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
			return nil, err
		}
		run, checks, err := s.store.GetRun(ctx, args.RunID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"run": run, "checks": checks}, nil
	case "scenario_slo":
		var args struct {
			Name   string
			Window string
		}
		_ = json.Unmarshal(p.Arguments, &args)
		window := time.Hour
		if args.Window != "" {
			if d, err := time.ParseDuration(args.Window); err == nil {
				window = d
			}
		}
		rates, err := s.store.SLOPassRate(ctx, args.Name, window)
		if err != nil {
			return nil, err
		}
		return map[string]any{"scenario": args.Name, "window": window.String(), "pass_rates": rates}, nil
	case "list_workers":
		return s.store.ListWorkers(ctx)
	}
	return nil, errUnknownTool(p.Name)
}

type unknownToolErr string

func (e unknownToolErr) Error() string { return "unknown tool: " + string(e) }

func errUnknownTool(name string) error { return unknownToolErr(name) }
