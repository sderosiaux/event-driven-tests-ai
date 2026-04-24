package httpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
)

// SSEEvent is one parsed Server-Sent Event.
//   - Name: the "event:" line value, empty when the stream uses data-only frames
//   - Data: concatenated "data:" lines, JSON-decoded when valid, otherwise the
//     raw string wrapped in a map for uniform CEL access
//   - ID:   the "id:" line value (last one seen), useful for last-event-id resume
type SSEEvent struct {
	Name string
	Data any
	ID   string
}

// SSEStream is an active Server-Sent Events read cursor. Close to free the
// underlying HTTP response.
type SSEStream struct {
	body io.ReadCloser
	br   *bufio.Reader
}

// StreamSSE issues GET <base>+path with Accept: text/event-stream and returns
// an SSEStream whose Next method yields parsed events until the server closes
// or ctx is cancelled.
func (c *Client) StreamSSE(ctx context.Context, path string, extraHeaders map[string]string) (*SSEStream, error) {
	url := c.base + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("sse: build request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	c.applyAuth(req)

	// SSE streams are long-lived; drop the client's default 30s timeout by
	// using a one-shot transport call bypassed of the Timeout field. We do
	// this by dialing a fresh http.Client without the timeout — the step's
	// own deadline governs cancellation.
	streamClient := &http.Client{Transport: c.http.Transport}
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sse: connect: %w", err)
	}
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		resp.Body.Close()
		return nil, fmt.Errorf("sse: %s returned %d: %s", path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		resp.Body.Close()
		return nil, fmt.Errorf("sse: expected Content-Type text/event-stream, got %q", ct)
	}
	return &SSEStream{body: resp.Body, br: bufio.NewReader(resp.Body)}, nil
}

// Next blocks until the next complete event is received, the stream closes
// (io.EOF), or ctx is cancelled. The SSE framing is: lines prefixed with
// "event:", "data:", "id:", separated by a blank line. We parse only those;
// retry/comment lines are ignored.
func (s *SSEStream) Next(ctx context.Context) (SSEEvent, error) {
	if ctx.Err() != nil {
		return SSEEvent{}, ctx.Err()
	}
	var ev SSEEvent
	var dataParts []string
	for {
		line, err := s.br.ReadString('\n')
		if err != nil {
			if len(dataParts) == 0 {
				return SSEEvent{}, err
			}
			// Final partial frame: deliver it before surfacing EOF on next call.
			ev.Data = decodeSSEData(dataParts)
			return ev, nil
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			// Blank line = end of event.
			if len(dataParts) == 0 {
				// Skip empty frames (keep-alive comments, leading blanks).
				continue
			}
			ev.Data = decodeSSEData(dataParts)
			return ev, nil
		}
		if strings.HasPrefix(line, ":") {
			// Comment / keep-alive heartbeat.
			continue
		}
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			ev.Name = value
		case "data":
			dataParts = append(dataParts, value)
		case "id":
			ev.ID = value
		}
	}
}

// Close releases the underlying HTTP body. Safe to call multiple times.
func (s *SSEStream) Close() error { return s.body.Close() }

// decodeSSEData joins the data lines with "\n" per the SSE spec and attempts
// JSON decode. Non-JSON payloads are returned as the raw string so the
// orchestrator's event store carries something CEL-addressable in both cases.
func decodeSSEData(parts []string) any {
	raw := strings.Join(parts, "\n")
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err == nil {
		return v
	}
	return map[string]any{"_raw": raw}
}

// SSEConfig is what the orchestrator hands to a stream-open call. Mirrors
// the connector + step shape so the port stays narrow.
type SSEConfig struct {
	Connector *scenario.HTTPConnector
	Step      *scenario.SSEStep
}
