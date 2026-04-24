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

// PushReportRaw POSTs a pre-encoded eval payload to /api/v1/runs. The
// control-plane ingest is permissive: any JSON with scenario + run_id is
// accepted, so eval results can ride the same endpoint while a dedicated wire
// shape matures.
func (c *Client) PushReportRaw(ctx context.Context, scenarioName string, body []byte) error {
	payload, err := json.Marshal(map[string]any{
		"scenario": scenarioName,
		"run_id":   "eval-" + time.Now().UTC().Format("20060102T150405"),
		"mode":     "eval",
		"started_at": time.Now().UTC(),
		"finished_at": time.Now().UTC(),
		"raw":      string(body),
	})
	if err != nil {
		return err
	}
	return c.post(ctx, "/api/v1/runs", payload)
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
