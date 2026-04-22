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

	"github.com/event-driven-tests-ai/edt/pkg/report"
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
