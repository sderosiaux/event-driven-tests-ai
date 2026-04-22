// Package worker is the long-running edt agent that registers with a control
// plane, heartbeats, picks up assigned scenarios, and runs them in watch mode.
//
// M2 ships the protocol and loop scaffolding; the actual watch-mode runner
// arrives in M2-T7. Until then, an assigned scenario is fetched and executed
// once via the existing run-mode orchestrator (good enough to demonstrate the
// full register → heartbeat → fetch → execute → push report cycle).
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to the control-plane HTTP API on behalf of the worker process.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// RegisterResponse mirrors the control plane's wire shape.
type RegisterResponse struct {
	WorkerID string `json:"worker_id"`
	Token    string `json:"token,omitempty"`
}

func (c *Client) Register(ctx context.Context, labels map[string]string, version string) (RegisterResponse, error) {
	body, _ := json.Marshal(map[string]any{"labels": labels, "version": version})
	var out RegisterResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/workers/register", body, &out)
	return out, err
}

// Assignment is one scenario the control plane assigned to this worker.
type Assignment struct {
	Scenario   string `json:"scenario"`
	AssignedAt string `json:"assigned_at"`
}

// HeartbeatResponse is what /heartbeat returns.
type HeartbeatResponse struct {
	WorkerID    string       `json:"worker_id"`
	Assignments []Assignment `json:"assignments"`
}

func (c *Client) Heartbeat(ctx context.Context, workerID string) (HeartbeatResponse, error) {
	var out HeartbeatResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/workers/"+workerID+"/heartbeat", nil, &out)
	return out, err
}

// FetchScenario returns the YAML body of a named scenario.
func (c *Client) FetchScenario(ctx context.Context, name string) ([]byte, error) {
	var raw map[string]any
	if err := c.do(ctx, http.MethodGet, "/api/v1/scenarios/"+name, nil, &raw); err != nil {
		return nil, err
	}
	yaml, ok := raw["yaml"].(string)
	if !ok {
		return nil, fmt.Errorf("worker: scenario response missing yaml field")
	}
	return []byte(yaml), nil
}

// ScenarioSample mirrors the wire shape returned by GET /scenarios/{name}/state.
type ScenarioSample struct {
	Check    string    `json:"check"`
	Ts       time.Time `json:"ts"`
	Passed   bool      `json:"passed"`
	Value    string    `json:"value"`
	Severity string    `json:"severity"`
	Window   string    `json:"window"`
}

type scenarioStateResponse struct {
	Scenario string           `json:"scenario"`
	Since    time.Time        `json:"since"`
	Samples  []ScenarioSample `json:"samples"`
}

// FetchScenarioState fetches recent check samples for a scenario so a resumed
// worker can warm its windowed state. window controls how far back to pull.
func (c *Client) FetchScenarioState(ctx context.Context, name string, window time.Duration) ([]ScenarioSample, error) {
	path := fmt.Sprintf("/api/v1/scenarios/%s/state?window=%s", name, window.String())
	var resp scenarioStateResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Samples, nil
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, out any) error {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("worker: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("worker: control plane returned %d on %s %s: %s", resp.StatusCode, method, path, string(raw))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
