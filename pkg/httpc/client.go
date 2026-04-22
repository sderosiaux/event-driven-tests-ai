// Package httpc implements the HTTP connector used by HTTPStep.
//
// Named httpc to avoid shadowing the stdlib net/http package.
package httpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/event-driven-tests-ai/edt/pkg/scenario"
)

// Client wraps a net/http.Client with the connector-level configuration
// (base URL, default auth) and exposes Do(step) returning a normalized Response.
type Client struct {
	base string
	auth *scenario.HTTPAuth
	http *http.Client
}

// NewClient builds a Client from the connector config. A nil connector yields
// a client with no base URL and a default 30s timeout — useful for tests.
func NewClient(c *scenario.HTTPConnector) *Client {
	cl := &Client{http: &http.Client{Timeout: 30 * time.Second}}
	if c != nil {
		cl.base = strings.TrimRight(c.BaseURL, "/")
		cl.auth = c.Auth
	}
	return cl
}

// Response is the normalized result of a single HTTP step.
type Response struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    any               `json:"body,omitempty"`     // decoded JSON when content-type matches
	BodyRaw string            `json:"body_raw,omitempty"` // string fallback for non-JSON
	Latency time.Duration     `json:"latency"`
}

// Do executes a step and returns its Response. It does not evaluate `expect`;
// callers run CheckExpectation against the returned Response separately so the
// orchestrator can record the raw event before deciding pass/fail.
func (c *Client) Do(ctx context.Context, step *scenario.HTTPStep) (*Response, error) {
	if step == nil {
		return nil, fmt.Errorf("httpc: nil step")
	}
	method := strings.ToUpper(step.Method)
	if method == "" {
		method = "GET"
	}

	url := step.Path
	if c.base != "" && strings.HasPrefix(step.Path, "/") {
		url = c.base + step.Path
	}

	var body io.Reader
	if step.Body != "" {
		body = strings.NewReader(step.Body)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("httpc: build request: %w", err)
	}
	for k, v := range step.Headers {
		req.Header.Set(k, v)
	}
	c.applyAuth(req)

	t0 := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("httpc: do %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()
	latency := time.Since(t0)

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("httpc: read body: %w", err)
	}

	headers := make(map[string]string, len(resp.Header))
	for k := range resp.Header {
		headers[k] = resp.Header.Get(k)
	}

	r := &Response{
		Status:  resp.StatusCode,
		Headers: headers,
		BodyRaw: string(raw),
		Latency: latency,
	}
	if isJSON(headers["Content-Type"]) {
		var v any
		if err := json.Unmarshal(raw, &v); err == nil {
			r.Body = v
		}
	}
	return r, nil
}

func (c *Client) applyAuth(req *http.Request) {
	if c.auth == nil {
		return
	}
	switch c.auth.Type {
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+c.auth.Token)
	case "basic":
		req.SetBasicAuth(c.auth.User, c.auth.Pass)
	}
}

func isJSON(ct string) bool {
	return strings.Contains(strings.ToLower(ct), "json")
}

// CheckExpectation evaluates a step's expect block against a Response.
// Returns nil on match, otherwise an error describing the first mismatch.
func CheckExpectation(exp *scenario.HTTPExpect, r *Response) error {
	if exp == nil {
		return nil
	}
	if exp.Status != 0 && r.Status != exp.Status {
		return fmt.Errorf("status: want %d, got %d", exp.Status, r.Status)
	}
	for k, want := range exp.Headers {
		if got := r.Headers[k]; got != want {
			return fmt.Errorf("header %q: want %q, got %q", k, want, got)
		}
	}
	if len(exp.Body) > 0 {
		got, ok := r.Body.(map[string]any)
		if !ok {
			return fmt.Errorf("body: expected JSON object, got %T", r.Body)
		}
		for k, want := range exp.Body {
			if !equalAny(got[k], want) {
				return fmt.Errorf("body field %q: want %v, got %v", k, want, got[k])
			}
		}
	}
	return nil
}

func equalAny(a, b any) bool {
	// Best-effort: round-trip via JSON to normalize numeric types.
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return bytes.Equal(ab, bb)
}
