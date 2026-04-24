package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/events"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
)

// runGRPC invokes a single unary RPC against the scenario's gRPC connector
// and persists the result into the events store under stream name
// "grpc:<method>". Expect-mismatches surface as errors so CI can fail the
// scenario; a gRPC status code != 0 with no Expect block is recorded but
// does NOT fail the step (same as HTTPStep) — the author can assert on it
// through Expect.Code or a CEL check.
func (r *Runner) runGRPC(ctx context.Context, step *scenario.Step) error {
	if r.GRPC == nil {
		return fmt.Errorf("grpc: no client configured — set spec.connectors.grpc")
	}
	grpcStep := step.GRPC
	if grpcStep == nil || grpcStep.Method == "" {
		return fmt.Errorf("grpc: step %q missing method", step.Name)
	}
	if r.active == nil || r.active.Spec.Connectors.GRPC == nil {
		return fmt.Errorf("grpc: scenario.connectors.grpc is required for step %q", step.Name)
	}

	// Apply ${...} interpolation to the request body so scenarios can inject
	// values from previous steps (e.g. an ID returned by an earlier RPC).
	resolved := *grpcStep
	resolved.Request = r.interp.expand(grpcStep.Request)

	resp, err := r.GRPC.Invoke(ctx, r.active.Spec.Connectors.GRPC, &resolved)
	if err != nil {
		return fmt.Errorf("grpc: invoke %s: %w", grpcStep.Method, err)
	}

	direction := events.Consumed
	if resp.Code != 0 {
		direction = events.ProducedFailed
	}
	r.Store.Append(events.Event{
		Stream: "grpc:" + grpcStep.Method,
		Ts:     time.Now().UTC(),
		Payload: map[string]any{
			"code":    resp.Code,
			"message": resp.Message,
			"body":    resp.Body,
		},
		Direction: direction,
	})
	r.interp.recordPrevious(previousFromPayload("", resp.Body))

	if err := checkGRPCExpect(grpcStep.Expect, resp); err != nil {
		return fmt.Errorf("grpc: %s: %w", grpcStep.Method, err)
	}
	return nil
}

// checkGRPCExpect compares the response to a scenario-declared expectation.
// Nil expect is a pass. When code is set, strict equality. When body is set,
// each key/value pair must match the response body at the top level.
func checkGRPCExpect(expect *scenario.GRPCExpect, resp *GRPCResponse) error {
	if expect == nil {
		return nil
	}
	if resp.Code != expect.Code {
		return fmt.Errorf("expected code %d, got %d (%s)", expect.Code, resp.Code, resp.Message)
	}
	for k, want := range expect.Body {
		got, ok := resp.Body[k]
		if !ok {
			return fmt.Errorf("expected body field %q missing from response", k)
		}
		if !equalAny(got, want) {
			return fmt.Errorf("body field %q: expected %v, got %v", k, want, got)
		}
	}
	return nil
}

// equalAny compares two JSON-shaped values. Numbers are normalised through
// float64 so a JSON-decoded number matches a Go literal.
func equalAny(got, want any) bool {
	gn, gok := toFloat(got)
	wn, wok := toFloat(want)
	if gok && wok {
		return gn == wn
	}
	return fmt.Sprint(got) == fmt.Sprint(want)
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}
