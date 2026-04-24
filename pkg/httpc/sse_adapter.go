package httpc

import (
	"context"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
)

// Session is the runtime view of an active SSE subscription. Satisfied by
// *SSEStream; test doubles satisfy the same two methods.
type Session interface {
	Next(ctx context.Context) (SSEEvent, error)
	Close() error
}

// SSEAdapter wraps a Client so the orchestrator can drive it through an
// SSEPort interface without pkg/orchestrator importing pkg/httpc types.
type SSEAdapter struct {
	client *Client
}

// NewSSEAdapter returns an adapter that uses c for all SSE streams.
func NewSSEAdapter(c *Client) *SSEAdapter { return &SSEAdapter{client: c} }

// Open issues the SSE GET and hands the live stream to the orchestrator.
func (a *SSEAdapter) Open(ctx context.Context, _ *scenario.HTTPConnector, step *scenario.SSEStep) (Session, error) {
	stream, err := a.client.StreamSSE(ctx, step.Path, nil)
	if err != nil {
		return nil, err
	}
	return stream, nil
}
