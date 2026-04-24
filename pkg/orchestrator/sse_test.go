package orchestrator_test

import (
	"context"
	"testing"
	"time"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/events"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/httpc"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/orchestrator"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSSEPort feeds canned SSE events into the orchestrator without opening
// a real HTTP connection.
type fakeSSEPort struct {
	events []httpc.SSEEvent
	opened bool
	closed bool
	openErr error
}

func (p *fakeSSEPort) Open(_ context.Context, _ *scenario.HTTPConnector, _ *scenario.SSEStep) (httpc.Session, error) {
	p.opened = true
	if p.openErr != nil {
		return nil, p.openErr
	}
	return &fakeSSESession{parent: p}, nil
}

type fakeSSESession struct {
	parent *fakeSSEPort
	idx    int
}

func (s *fakeSSESession) Next(ctx context.Context) (httpc.SSEEvent, error) {
	if s.idx >= len(s.parent.events) {
		<-ctx.Done()
		return httpc.SSEEvent{}, ctx.Err()
	}
	ev := s.parent.events[s.idx]
	s.idx++
	return ev, nil
}

func (s *fakeSSESession) Close() error {
	s.parent.closed = true
	return nil
}

func sseScenario(step scenario.Step) *scenario.Scenario {
	return &scenario.Scenario{
		Spec: scenario.Spec{
			Connectors: scenario.Connectors{
				HTTP: &scenario.HTTPConnector{BaseURL: "http://fake"},
			},
			Steps: []scenario.Step{step},
		},
	}
}

func TestSSEStepMatchesAndStops(t *testing.T) {
	store := events.NewMemStore(0)
	sse := &fakeSSEPort{events: []httpc.SSEEvent{
		{Name: "order", Data: map[string]any{"state": "placed"}},
		{Name: "order", Data: map[string]any{"state": "shipped"}},
		{Name: "order", Data: map[string]any{"state": "late-arrival-never-read"}},
	}}
	r := orchestrator.New(&scenario.Scenario{}, nil, nil, store)
	r.SSE = sse

	s := sseScenario(scenario.Step{
		Name: "sse",
		SSE: &scenario.SSEStep{
			Path:    "/orders/stream",
			Timeout: "1s",
			Match:   []scenario.MatchRule{{Key: `payload.state == "shipped"`}},
		},
	})
	require.NoError(t, r.Run(context.Background(), s))
	assert.Len(t, store.Query("sse:/orders/stream"), 2, "stops after the matching event")
	assert.True(t, sse.closed)
}

func TestSSEStepCountBound(t *testing.T) {
	store := events.NewMemStore(0)
	sse := &fakeSSEPort{events: []httpc.SSEEvent{
		{Data: map[string]any{"n": 1}},
		{Data: map[string]any{"n": 2}},
		{Data: map[string]any{"n": 3}},
	}}
	r := orchestrator.New(&scenario.Scenario{}, nil, nil, store)
	r.SSE = sse

	s := sseScenario(scenario.Step{
		Name: "sse",
		SSE: &scenario.SSEStep{Path: "/x", Count: 2, Timeout: "1s"},
	})
	require.NoError(t, r.Run(context.Background(), s))
	assert.Len(t, store.Query("sse:/x"), 2)
}

func TestSSEStepTimeoutWithNeverMatchingRuleErrors(t *testing.T) {
	store := events.NewMemStore(0)
	sse := &fakeSSEPort{events: []httpc.SSEEvent{
		{Data: map[string]any{"state": "unrelated"}},
	}}
	r := orchestrator.New(&scenario.Scenario{}, nil, nil, store)
	r.SSE = sse

	s := sseScenario(scenario.Step{
		Name: "sse",
		SSE: &scenario.SSEStep{
			Path:    "/watch",
			Timeout: "100ms",
			Match:   []scenario.MatchRule{{Key: `payload.state == "target"`}},
		},
	})
	err := r.Run(context.Background(), s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
}

func TestSSEStepRejectsMissingConnector(t *testing.T) {
	store := events.NewMemStore(0)
	r := orchestrator.New(&scenario.Scenario{}, nil, nil, store)
	r.SSE = &fakeSSEPort{}
	s := &scenario.Scenario{Spec: scenario.Spec{Steps: []scenario.Step{
		{Name: "sse", SSE: &scenario.SSEStep{Path: "/x", Count: 1, Timeout: "100ms"}},
	}}}
	err := r.Run(context.Background(), s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connectors.http")
}

func TestSSEStepRejectsMissingPort(t *testing.T) {
	store := events.NewMemStore(0)
	r := orchestrator.New(&scenario.Scenario{}, nil, nil, store)
	s := sseScenario(scenario.Step{Name: "sse", SSE: &scenario.SSEStep{Path: "/x", Count: 1, Timeout: "100ms"}})
	err := r.Run(context.Background(), s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no client configured")
}

func TestSSEStepQuietStreamReturnsWithoutMatcher(t *testing.T) {
	store := events.NewMemStore(0)
	sse := &fakeSSEPort{events: []httpc.SSEEvent{{Data: map[string]any{"x": 1}}}}
	r := orchestrator.New(&scenario.Scenario{}, nil, nil, store)
	r.SSE = sse
	t0 := time.Now()
	s := sseScenario(scenario.Step{
		Name: "sse",
		SSE:  &scenario.SSEStep{Path: "/quiet", Count: 5, Timeout: "120ms"},
	})
	require.NoError(t, r.Run(context.Background(), s))
	assert.GreaterOrEqual(t, time.Since(t0), 80*time.Millisecond)
	assert.Len(t, store.Query("sse:/quiet"), 1)
}
