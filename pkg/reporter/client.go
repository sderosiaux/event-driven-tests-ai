// Package reporter pushes a finalized report.Report to the control plane.
//
// Network errors never alter the CLI exit code — losing the control plane
// must not turn a green CI build red. Errors are returned to the caller for
// logging only.
package reporter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/report"
)

// Client posts reports to a control plane endpoint.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New builds a Client. baseURL must point at the control plane root, e.g.
// "http://cp:8080". A trailing slash is tolerated.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

// EvalRunRequest mirrors api.evalRunRequest on the write side. Ship it to
// PushEvalRun to persist an `edt eval` report as a first-class row in the
// eval_runs + eval_results tables.
type EvalRunRequest struct {
	ID         string            `json:"id"`
	Scenario   string            `json:"scenario"`
	JudgeModel string            `json:"judge_model,omitempty"`
	Iterations int               `json:"iterations,omitempty"`
	StartedAt  time.Time         `json:"started_at"`
	FinishedAt time.Time         `json:"finished_at"`
	Status     string            `json:"status"`
	Results    []EvalResultWire  `json:"results"`
}

// EvalResultWire is the per-eval row inside an EvalRunRequest. Mirrors the
// storage.EvalResult shape on the wire.
type EvalResultWire struct {
	Name            string  `json:"name"`
	JudgeModel      string  `json:"judge_model,omitempty"`
	Aggregate       string  `json:"aggregate,omitempty"`
	Samples         int     `json:"samples"`
	RequiredSamples int     `json:"required_samples,omitempty"`
	Value           float64 `json:"value"`
	Threshold       string  `json:"threshold,omitempty"`
	Passed          bool    `json:"passed"`
	Status          string  `json:"status"`
	Errors          int     `json:"errors,omitempty"`
}

// PushEvalRun POSTs a typed eval report to /api/v1/eval-runs.
func (c *Client) PushEvalRun(ctx context.Context, req EvalRunRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("reporter: marshal eval-run: %w", err)
	}
	return c.post(ctx, "/api/v1/eval-runs", body)
}

func (c *Client) post(ctx context.Context, path string, body []byte) error {
	if c.baseURL == "" {
		return fmt.Errorf("reporter: empty baseURL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("reporter: post %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("reporter: control plane returned %d on %s: %s", resp.StatusCode, path, string(raw))
	}
	return nil
}

// PushReport POSTs the report JSON to /api/v1/runs.
func (c *Client) PushReport(ctx context.Context, r *report.Report) error {
	if c.baseURL == "" {
		return fmt.Errorf("reporter: empty baseURL")
	}
	body, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("reporter: marshal report: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/runs", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("reporter: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("reporter: post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("reporter: control plane returned %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}
