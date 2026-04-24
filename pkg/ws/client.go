// Package ws wraps coder/websocket for the orchestrator's WebSocket step.
//
// Two orthogonal surfaces are intentional:
//   - a thin Client the orchestrator drives (Dial, Send, Read, Close)
//   - a narrow contract the test fakes can satisfy without the real library
//
// Non-goals: server-side accept, custom subprotocols, compression tuning.
// Scenarios that need those should run the raw coder/websocket library
// themselves via a custom worker image.
package ws

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// Client is an edt-facing wrapper over a WebSocket connection. One client per
// step; callers are responsible for calling Close when the step finishes.
type Client struct {
	conn *websocket.Conn
}

// Adapter plugs pkg/ws into orchestrator.WebSocketPort. It dials on demand;
// one session per WebSocketStep, closed by the orchestrator when the step
// finishes. One Adapter instance is enough per scenario run.
type Adapter struct{}

// NewAdapter returns the default implementation of orchestrator.WebSocketPort.
func NewAdapter() *Adapter { return &Adapter{} }

// Session is the runtime view the orchestrator drives. Satisfied by *Client
// plus any caller-provided fake that speaks the same three methods.
type Session interface {
	SendJSON(ctx context.Context, v any) error
	Read(ctx context.Context) (Message, error)
	Close()
}

// Open dials the configured endpoint and returns a session the orchestrator
// can drive. Auth headers are resolved from the connector's HTTPAuth block so
// the WebSocket upgrade carries the same bearer/basic credentials the REST
// side of the service would accept.
func (Adapter) Open(ctx context.Context, conn *scenario.WebSocketConnector, step *scenario.WebSocketStep) (Session, error) {
	if conn == nil {
		return nil, fmt.Errorf("ws: connector is nil — scenario is missing spec.connectors.websocket")
	}
	var headers http.Header
	if conn.Auth != nil {
		headers = http.Header{}
		switch conn.Auth.Type {
		case "bearer":
			headers.Set("Authorization", "Bearer "+conn.Auth.Token)
		case "basic":
			// Base64 encoding happens here so the fetcher stays idiomatic on the
			// WebSocket upgrade, which has no SDK-native basic-auth helper.
			headers.Set("Authorization", "Basic "+basicAuth(conn.Auth.User, conn.Auth.Pass))
		}
	}
	return Dial(ctx, Config{
		BaseURL: conn.BaseURL,
		Path:    step.Path,
		Headers: headers,
	})
}

func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

// Config drives Dial. BaseURL must be a ws:// or wss:// URL; a trailing slash
// is tolerated. Path is joined with a single slash.
type Config struct {
	BaseURL string
	Path    string
	// Headers are sent with the HTTP upgrade. Typical use: authentication.
	Headers http.Header
	// Subprotocols are advertised in the Sec-WebSocket-Protocol header.
	Subprotocols []string
	// DialTimeout bounds the HTTP upgrade. Zero = 10s default.
	DialTimeout time.Duration
}

// Dial opens a WebSocket connection. The connection stays open until the
// caller invokes Close (or the ctx used in subsequent reads is cancelled).
func Dial(ctx context.Context, cfg Config) (*Client, error) {
	u, err := joinURL(cfg.BaseURL, cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("ws: build url: %w", err)
	}
	dialTimeout := cfg.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = 10 * time.Second
	}
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	conn, _, err := websocket.Dial(dialCtx, u, &websocket.DialOptions{
		HTTPHeader:   cfg.Headers,
		Subprotocols: cfg.Subprotocols,
	})
	if err != nil {
		return nil, fmt.Errorf("ws: dial %s: %w", u, err)
	}
	return &Client{conn: conn}, nil
}

// SendJSON writes a JSON-encoded message. Safe to call before Read.
func (c *Client) SendJSON(ctx context.Context, v any) error {
	if err := wsjson.Write(ctx, c.conn, v); err != nil {
		return fmt.Errorf("ws: send: %w", err)
	}
	return nil
}

// Message is a decoded inbound frame the orchestrator stores as an event.
type Message struct {
	Payload any
	Raw     []byte
}

// Read blocks for the next inbound message and returns it decoded. JSON frames
// yield map[string]any / []any / scalars; non-JSON frames fall back to the
// raw bytes wrapped in a map so the orchestrator's CEL matchers see a uniform
// shape across formats.
func (c *Client) Read(ctx context.Context) (Message, error) {
	msgType, body, err := c.conn.Read(ctx)
	if err != nil {
		return Message{}, fmt.Errorf("ws: read: %w", err)
	}
	if msgType == websocket.MessageText {
		var v any
		if err := json.Unmarshal(body, &v); err == nil {
			return Message{Payload: v, Raw: body}, nil
		}
	}
	return Message{Payload: map[string]any{"_raw": string(body)}, Raw: body}, nil
}

// Close ends the connection with a normal-closure status. Errors are
// swallowed — Close is always best-effort, matching the Kafka client's shape.
func (c *Client) Close() {
	if c.conn != nil {
		_ = c.conn.Close(websocket.StatusNormalClosure, "")
	}
}

// CloseWithStatus lets callers signal a non-normal reason (policy violation,
// protocol error, etc.) when the scenario's match loop rejects a frame.
func (c *Client) CloseWithStatus(status websocket.StatusCode, reason string) {
	if c.conn != nil {
		_ = c.conn.Close(status, reason)
	}
}

func joinURL(base, path string) (string, error) {
	base = strings.TrimRight(base, "/")
	if base == "" {
		return "", fmt.Errorf("empty base url")
	}
	if path == "" {
		return base, nil
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path, nil
}
