package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/events"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
)

// runWebSocket dials the WebSocket connector, optionally sends an initial
// JSON message, then reads inbound frames into the events store under
// stream name "ws:<path>" until one of:
//   - Count messages have been received (step.Count > 0)
//   - a Match rule matches (step.Match non-empty)
//   - the step timeout elapses
//   - ctx is cancelled
//
// A never-matching rule combined with a timeout is the canonical "this event
// should NOT appear" assertion, mirroring consume-step semantics.
func (r *Runner) runWebSocket(ctx context.Context, step *scenario.Step) error {
	if r.WebSocket == nil {
		return fmt.Errorf("websocket: no client configured — set spec.connectors.websocket")
	}
	ws := step.WebSocket
	if ws == nil || ws.Path == "" {
		return fmt.Errorf("websocket: step %q missing path", step.Name)
	}

	conn := r.connectorFor(step)
	if conn == nil {
		return fmt.Errorf("websocket: scenario.connectors.websocket is required for step %q", step.Name)
	}

	session, err := r.WebSocket.Open(ctx, conn, ws)
	if err != nil {
		return fmt.Errorf("websocket: open: %w", err)
	}
	defer session.Close()

	// Optional initial frame. interpolation resolves ${run.*} / ${previous.*}
	// before the payload hits the wire.
	if ws.Send != "" {
		var payload any
		expanded := r.interp.expand(ws.Send)
		payload, err := r.resolvePayload(expanded)
		if err != nil {
			return fmt.Errorf("websocket: send payload: %w", err)
		}
		if err := session.SendJSON(ctx, payload); err != nil {
			return err
		}
	}

	matcher, err := compileMatcher(ws.Match)
	if err != nil {
		return fmt.Errorf("websocket: compile match: %w", err)
	}

	timeout := parseTimeoutOrDefault(ws.Timeout, 30*time.Second)
	deadline := time.Now().Add(timeout)
	stream := "ws:" + ws.Path

	received := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			if matcher != nil {
				return fmt.Errorf("websocket: step %q timed out after %s with no match on %s", step.Name, timeout, stream)
			}
			return nil
		}
		readCtx, cancel := context.WithDeadline(ctx, deadline)
		msg, err := session.Read(readCtx)
		cancel()
		if err != nil {
			// Honour the step's declared deadline, not the library's error text.
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if time.Now().After(deadline) {
				if matcher != nil {
					return fmt.Errorf("websocket: step %q timed out after %s with no match on %s", step.Name, timeout, stream)
				}
				return nil
			}
			return fmt.Errorf("websocket: read: %w", err)
		}

		r.Store.Append(events.Event{
			Stream:    stream,
			Ts:        time.Now().UTC(),
			Payload:   msg.Payload,
			Direction: events.Consumed,
		})
		received++

		if matcher != nil {
			ok, merr := matcher.matches(msg.Payload, r.interp.previous, r.interp.run)
			if merr != nil {
				return fmt.Errorf("websocket: match: %w", merr)
			}
			if ok {
				r.interp.recordPrevious(previousFromPayload("", msg.Payload))
				return nil
			}
		}

		if ws.Count > 0 && received >= ws.Count {
			return nil
		}

		if ws.SlowMode != nil && ws.SlowMode.PauseEvery > 0 && received%ws.SlowMode.PauseEvery == 0 {
			pause, err := time.ParseDuration(ws.SlowMode.PauseFor)
			if err != nil {
				return fmt.Errorf("websocket: invalid slow_mode.pause_for %q: %w", ws.SlowMode.PauseFor, err)
			}
			select {
			case <-time.After(pause):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func (r *Runner) connectorFor(_ *scenario.Step) *scenario.WebSocketConnector {
	if r.active == nil {
		return nil
	}
	return r.active.Spec.Connectors.WebSocket
}

// parseTimeoutOrDefault returns d parsed, or def if the string is empty. On
// malformed input we fall back to def rather than error — the step's JSON
// Schema validation already ran before Run, so unreachable here in practice.
func parseTimeoutOrDefault(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}
	return def
}
