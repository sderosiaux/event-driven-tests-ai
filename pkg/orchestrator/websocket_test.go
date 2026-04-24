package orchestrator_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/events"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/orchestrator"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/ws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeWS feeds canned inbound frames into a session so orchestrator tests can
// drive runWebSocket without dialing a real server.
type fakeWS struct {
	frames     []ws.Message
	readErrOn  int  // return an error on this Read index
	errEnabled bool // guard so the zero-value "0" does not implicitly trigger on first read
	sent       []any
	openCalls  int
	closed     bool

	// If non-nil, Open returns this error immediately.
	openErr error
}

func (f *fakeWS) Open(_ context.Context, _ *scenario.WebSocketConnector, _ *scenario.WebSocketStep) (ws.Session, error) {
	f.openCalls++
	if f.openErr != nil {
		return nil, f.openErr
	}
	return &fakeSession{parent: f}, nil
}

type fakeSession struct {
	parent *fakeWS
	idx    int
}

func (s *fakeSession) SendJSON(_ context.Context, v any) error {
	s.parent.sent = append(s.parent.sent, v)
	return nil
}

func (s *fakeSession) Read(ctx context.Context) (ws.Message, error) {
	if s.parent.errEnabled && s.parent.readErrOn == s.idx {
		return ws.Message{}, errors.New("simulated read error")
	}
	if s.idx >= len(s.parent.frames) {
		// Block until ctx cancel so "timeout" semantics surface as ctx errors.
		<-ctx.Done()
		return ws.Message{}, ctx.Err()
	}
	m := s.parent.frames[s.idx]
	s.idx++
	return m, nil
}

func (s *fakeSession) Close() { s.parent.closed = true }

func wsScenario(step scenario.Step) *scenario.Scenario {
	return &scenario.Scenario{
		Spec: scenario.Spec{
			Connectors: scenario.Connectors{
				WebSocket: &scenario.WebSocketConnector{BaseURL: "ws://fake"},
			},
			Steps: []scenario.Step{step},
		},
	}
}

func TestWebSocketStepMatchesAndStops(t *testing.T) {
	store := events.NewMemStore(0)
	fk := &fakeWS{frames: []ws.Message{
		{Payload: map[string]any{"type": "noise"}},
		{Payload: map[string]any{"type": "target", "id": "42"}},
		{Payload: map[string]any{"type": "after-match-never-read"}},
	}}
	r := orchestrator.New(&scenario.Scenario{}, nil, nil, store)
	r.WebSocket = fk

	s := wsScenario(scenario.Step{
		Name: "ws",
		WebSocket: &scenario.WebSocketStep{
			Path:    "/feed",
			Timeout: "1s",
			Match:   []scenario.MatchRule{{Key: `payload.type == "target"`}},
		},
	})
	require.NoError(t, r.Run(context.Background(), s))

	events := store.Query("ws:/feed")
	require.Len(t, events, 2, "should stop reading after the matching frame")
	assert.True(t, fk.closed)
}

func TestWebSocketStepSendsInitialFrame(t *testing.T) {
	store := events.NewMemStore(0)
	fk := &fakeWS{frames: []ws.Message{
		{Payload: map[string]any{"ack": true}},
	}}
	r := orchestrator.New(&scenario.Scenario{}, nil, nil, store)
	r.WebSocket = fk

	s := wsScenario(scenario.Step{
		Name: "ws",
		WebSocket: &scenario.WebSocketStep{
			Path:    "/orders",
			Send:    `{"subscribe":"orders"}`,
			Count:   1,
			Timeout: "1s",
		},
	})
	require.NoError(t, r.Run(context.Background(), s))
	require.Len(t, fk.sent, 1, "initial send frame must be delivered")
}

func TestWebSocketStepCountBoundStopsEarly(t *testing.T) {
	store := events.NewMemStore(0)
	fk := &fakeWS{frames: []ws.Message{
		{Payload: map[string]any{"n": 1}},
		{Payload: map[string]any{"n": 2}},
		{Payload: map[string]any{"n": 3}},
		{Payload: map[string]any{"n": 4}},
	}}
	r := orchestrator.New(&scenario.Scenario{}, nil, nil, store)
	r.WebSocket = fk

	s := wsScenario(scenario.Step{
		Name: "ws",
		WebSocket: &scenario.WebSocketStep{
			Path:    "/counter",
			Count:   2,
			Timeout: "2s",
		},
	})
	require.NoError(t, r.Run(context.Background(), s))
	assert.Len(t, store.Query("ws:/counter"), 2, "count bound must stop at exactly 2 frames")
}

func TestWebSocketStepTimeoutWithNeverMatchingRuleErrors(t *testing.T) {
	store := events.NewMemStore(0)
	fk := &fakeWS{frames: []ws.Message{
		{Payload: map[string]any{"type": "unrelated"}},
	}}
	r := orchestrator.New(&scenario.Scenario{}, nil, nil, store)
	r.WebSocket = fk

	s := wsScenario(scenario.Step{
		Name: "ws",
		WebSocket: &scenario.WebSocketStep{
			Path:    "/feed",
			Timeout: "100ms",
			Match:   []scenario.MatchRule{{Key: `payload.type == "target"`}},
		},
	})
	err := r.Run(context.Background(), s)
	require.Error(t, err, "never-matching + timeout must bubble up as an error")
	assert.Contains(t, err.Error(), "timed out")
}

func TestWebSocketStepRejectsMissingConnector(t *testing.T) {
	store := events.NewMemStore(0)
	r := orchestrator.New(&scenario.Scenario{}, nil, nil, store)
	r.WebSocket = &fakeWS{}

	s := &scenario.Scenario{Spec: scenario.Spec{
		Steps: []scenario.Step{{
			Name:      "ws",
			WebSocket: &scenario.WebSocketStep{Path: "/x", Count: 1, Timeout: "100ms"},
		}},
	}}
	err := r.Run(context.Background(), s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connectors.websocket")
}

func TestWebSocketStepRejectsMissingPort(t *testing.T) {
	store := events.NewMemStore(0)
	r := orchestrator.New(&scenario.Scenario{}, nil, nil, store)
	// r.WebSocket intentionally nil
	s := wsScenario(scenario.Step{Name: "ws", WebSocket: &scenario.WebSocketStep{Path: "/x", Count: 1, Timeout: "100ms"}})
	err := r.Run(context.Background(), s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no client configured")
}

// Codex P1 2026-04-24: slow_mode used to sleep for pause_for even if the
// step deadline was shorter. A busy stream could overrun the declared
// timeout by up to pause_for. The cap must bound pause by remaining time.
func TestWebSocketStepSlowModeRespectsStepTimeout(t *testing.T) {
	store := events.NewMemStore(0)
	// Three frames trigger slow_mode on every one (pause_every=1), each
	// with a 10s pause_for. Step timeout is 100ms — total wall clock must
	// not exceed ~200ms even though three 10s sleeps were requested.
	fk := &fakeWS{frames: []ws.Message{
		{Payload: map[string]any{"n": 1}},
		{Payload: map[string]any{"n": 2}},
		{Payload: map[string]any{"n": 3}},
	}}
	r := orchestrator.New(&scenario.Scenario{}, nil, nil, store)
	r.WebSocket = fk

	s := wsScenario(scenario.Step{
		Name: "ws",
		WebSocket: &scenario.WebSocketStep{
			Path:     "/x",
			Count:    10, // never reached
			Timeout:  "100ms",
			SlowMode: &scenario.SlowMode{PauseEvery: 1, PauseFor: "10s"},
		},
	})
	t0 := time.Now()
	require.NoError(t, r.Run(context.Background(), s))
	elapsed := time.Since(t0)
	assert.Less(t, elapsed, 500*time.Millisecond, "slow_mode pause must not exceed remaining step budget")
}

// Regression guard: an unmatched count bound should not block forever if the
// stream dries up. The fake blocks on ctx.Done for "no more frames", which
// means waiting past the step timeout must surface as a clean return.
func TestWebSocketStepReturnsWhenStreamQuietAndNoMatcher(t *testing.T) {
	store := events.NewMemStore(0)
	fk := &fakeWS{frames: []ws.Message{{Payload: map[string]any{"one": 1}}}}
	r := orchestrator.New(&scenario.Scenario{}, nil, nil, store)
	r.WebSocket = fk

	t0 := time.Now()
	s := wsScenario(scenario.Step{
		Name: "ws",
		WebSocket: &scenario.WebSocketStep{
			Path:    "/quiet",
			Count:   5, // never reached — only one frame available
			Timeout: "150ms",
		},
	})
	err := r.Run(context.Background(), s)
	require.NoError(t, err, "timeout without a matcher must return cleanly")
	assert.GreaterOrEqual(t, time.Since(t0), 100*time.Millisecond)
	assert.Len(t, store.Query("ws:/quiet"), 1)
}
