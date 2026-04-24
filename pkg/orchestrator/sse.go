package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/events"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
)

// runSSE opens a Server-Sent Events stream against the HTTP connector and
// records each inbound event into the store under stream name "sse:<path>".
//
// Semantics match runWebSocket:
//   - Count bound (0 = wait for first match or timeout)
//   - step.Match rules evaluated per event, first hit wins
//   - Timeout is the absolute step budget; a never-matching rule combined
//     with a timeout surfaces as an error so CI fails loudly
//   - SlowMode pauses every N events for pause_for
func (r *Runner) runSSE(ctx context.Context, step *scenario.Step) error {
	if r.SSE == nil {
		return fmt.Errorf("sse: no client configured — set spec.connectors.http and wire an SSEPort")
	}
	sse := step.SSE
	if sse == nil || sse.Path == "" {
		return fmt.Errorf("sse: step %q missing path", step.Name)
	}
	if r.active == nil || r.active.Spec.Connectors.HTTP == nil {
		return fmt.Errorf("sse: scenario.connectors.http is required for step %q", step.Name)
	}

	timeout := parseTimeoutOrDefault(sse.Timeout, 30*time.Second)
	deadline := time.Now().Add(timeout)

	openCtx, cancelOpen := context.WithDeadline(ctx, deadline)
	session, err := r.SSE.Open(openCtx, r.active.Spec.Connectors.HTTP, sse)
	cancelOpen()
	if err != nil {
		return fmt.Errorf("sse: open: %w", err)
	}
	defer session.Close()

	matcher, err := compileMatcher(sse.Match)
	if err != nil {
		return fmt.Errorf("sse: compile match: %w", err)
	}

	stream := "sse:" + sse.Path
	received := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			if matcher != nil {
				return fmt.Errorf("sse: step %q timed out after %s with no match on %s", step.Name, timeout, stream)
			}
			return nil
		}
		readCtx, cancel := context.WithDeadline(ctx, deadline)
		ev, err := session.Next(readCtx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if time.Now().After(deadline) {
				if matcher != nil {
					return fmt.Errorf("sse: step %q timed out after %s with no match on %s", step.Name, timeout, stream)
				}
				return nil
			}
			return fmt.Errorf("sse: read: %w", err)
		}

		r.Store.Append(events.Event{
			Stream:    stream,
			Ts:        time.Now().UTC(),
			Payload:   ev.Data,
			Direction: events.Consumed,
		})
		received++

		if matcher != nil {
			ok, merr := matcher.matches(ev.Data, r.interp.previous, r.interp.run)
			if merr != nil {
				return fmt.Errorf("sse: match: %w", merr)
			}
			if ok {
				r.interp.recordPrevious(previousFromPayload(ev.ID, ev.Data))
				return nil
			}
		}

		if sse.Count > 0 && received >= sse.Count {
			return nil
		}

		if sse.SlowMode != nil && sse.SlowMode.PauseEvery > 0 && received%sse.SlowMode.PauseEvery == 0 {
			pause, err := time.ParseDuration(sse.SlowMode.PauseFor)
			if err != nil {
				return fmt.Errorf("sse: invalid slow_mode.pause_for %q: %w", sse.SlowMode.PauseFor, err)
			}
			select {
			case <-time.After(pause):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}
