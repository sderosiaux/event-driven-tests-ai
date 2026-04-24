package grpcc

import (
	"context"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
)

// Invocation is the narrow shape the orchestrator consumes. Mirrored in
// pkg/orchestrator.GRPCResponse via a structural match.
type Invocation struct {
	Code    int
	Message string
	Body    map[string]any
}

// Port dials the configured connector on the first call and reuses the
// grpc.ClientConn across RPCs that target the same connector within one
// scenario run. Close releases it.
//
// Codex P2 2026-04-24: the cache key must include address + tls + auth so a
// scenario with steps pointing at two different gRPC servers doesn't
// silently send the second call to the first server.
type Port struct {
	cached    *Client
	cachedKey string
}

// NewPort returns an orchestrator-facing adapter. One instance per scenario
// run; the first Invoke dials, subsequent ones reuse the connection when the
// connector hasn't changed.
func NewPort() *Port { return &Port{} }

// Close must be called when the scenario finishes. It is safe to call more
// than once.
func (p *Port) Close() {
	if p.cached != nil {
		p.cached.Close()
		p.cached = nil
		p.cachedKey = ""
	}
}

// Invoke runs one unary RPC. Returns an Invocation even on non-OK status so
// the orchestrator can record the code+message as a structured event (HTTP
// parity — a 503 is a response, not an error from the client's perspective).
func (p *Port) Invoke(ctx context.Context, conn *scenario.GRPCConnector, step *scenario.GRPCStep) (*Invocation, error) {
	key := connectorKey(conn)
	if p.cached != nil && p.cachedKey != key {
		// Connector changed — close the stale conn before redialing so we
		// don't leak a background ClientConn for the scenario's lifetime.
		p.cached.Close()
		p.cached = nil
		p.cachedKey = ""
	}
	if p.cached == nil {
		cli, err := Dial(ctx, conn)
		if err != nil {
			return nil, err
		}
		p.cached = cli
		p.cachedKey = key
	}
	resp, err := p.cached.Invoke(ctx, step)
	if err != nil {
		return nil, err
	}
	return &Invocation{Code: resp.Code, Message: resp.Message, Body: resp.Body}, nil
}

// connectorKey captures every field that changes the dial target or
// credentials so the Port redials on any semantic change. Token values
// participate in the key but never in log output.
func connectorKey(c *scenario.GRPCConnector) string {
	if c == nil {
		return ""
	}
	authType, authTok := "", ""
	if c.Auth != nil {
		authType = c.Auth.Type
		authTok = c.Auth.Token
	}
	return c.Address + "|tls=" + boolString(c.TLS) + "|auth=" + authType + ":" + authTok
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
