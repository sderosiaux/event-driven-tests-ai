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
// grpc.ClientConn across RPCs within one scenario run. Close releases it.
type Port struct {
	cached *Client
}

// NewPort returns an orchestrator-facing adapter. One instance per scenario
// run; the first Invoke dials, subsequent ones reuse the connection.
func NewPort() *Port { return &Port{} }

// Close must be called when the scenario finishes. It is safe to call more
// than once.
func (p *Port) Close() {
	if p.cached != nil {
		p.cached.Close()
		p.cached = nil
	}
}

// Invoke runs one unary RPC. Returns an Invocation even on non-OK status so
// the orchestrator can record the code+message as a structured event (HTTP
// parity — a 503 is a response, not an error from the client's perspective).
func (p *Port) Invoke(ctx context.Context, conn *scenario.GRPCConnector, step *scenario.GRPCStep) (*Invocation, error) {
	if p.cached == nil {
		cli, err := Dial(ctx, conn)
		if err != nil {
			return nil, err
		}
		p.cached = cli
	}
	resp, err := p.cached.Invoke(ctx, step)
	if err != nil {
		return nil, err
	}
	return &Invocation{Code: resp.Code, Message: resp.Message, Body: resp.Body}, nil
}
