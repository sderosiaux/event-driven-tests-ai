package orchestrator_test

import (
	"context"
	"errors"
	"testing"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/events"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/grpcc"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/orchestrator"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeGRPCPort returns a canned Invocation for every call, letting tests
// isolate orchestrator behaviour from the protobuf wire.
type fakeGRPCPort struct {
	resp    *grpcc.Invocation
	err     error
	calls   int
	lastReq *scenario.GRPCStep
}

func (p *fakeGRPCPort) Invoke(_ context.Context, _ *scenario.GRPCConnector, step *scenario.GRPCStep) (*grpcc.Invocation, error) {
	p.calls++
	p.lastReq = step
	return p.resp, p.err
}

func grpcScenario(step scenario.Step) *scenario.Scenario {
	return &scenario.Scenario{
		Spec: scenario.Spec{
			Connectors: scenario.Connectors{
				GRPC: &scenario.GRPCConnector{Address: "localhost:0"},
			},
			Steps: []scenario.Step{step},
		},
	}
}

func TestGRPCStepRecordsSuccessEvent(t *testing.T) {
	store := events.NewMemStore(0)
	fake := &fakeGRPCPort{resp: &grpcc.Invocation{Code: 0, Body: map[string]any{"status": "SERVING"}}}
	r := orchestrator.New(&scenario.Scenario{}, nil, nil, store)
	r.GRPC = fake

	s := grpcScenario(scenario.Step{
		Name: "health",
		GRPC: &scenario.GRPCStep{
			Proto:   "syntax = \"proto3\"; message M {}",
			Method:  "grpc.health.v1.Health/Check",
			Request: `{"service":"edt"}`,
		},
	})
	require.NoError(t, r.Run(context.Background(), s))

	recs := store.Query("grpc:grpc.health.v1.Health/Check")
	require.Len(t, recs, 1)
	payload := recs[0].Payload.(map[string]any)
	assert.Equal(t, 0, payload["code"])
	assert.Equal(t, events.Consumed, recs[0].Direction)
}

func TestGRPCStepExpectCodeMismatchFails(t *testing.T) {
	store := events.NewMemStore(0)
	fake := &fakeGRPCPort{resp: &grpcc.Invocation{Code: 2, Message: "internal"}}
	r := orchestrator.New(&scenario.Scenario{}, nil, nil, store)
	r.GRPC = fake

	s := grpcScenario(scenario.Step{
		Name: "broken",
		GRPC: &scenario.GRPCStep{
			Proto:   "x",
			Method:  "pkg.Svc/Broken",
			Request: `{}`,
			Expect:  &scenario.GRPCExpect{Code: 0},
		},
	})
	err := r.Run(context.Background(), s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected code 0")
}

func TestGRPCStepExpectBodyFieldMatch(t *testing.T) {
	store := events.NewMemStore(0)
	fake := &fakeGRPCPort{resp: &grpcc.Invocation{Code: 0, Body: map[string]any{"status": "SERVING", "service": "edt"}}}
	r := orchestrator.New(&scenario.Scenario{}, nil, nil, store)
	r.GRPC = fake

	s := grpcScenario(scenario.Step{
		Name: "ok",
		GRPC: &scenario.GRPCStep{
			Proto:   "x",
			Method:  "pkg.Svc/Check",
			Request: `{}`,
			Expect:  &scenario.GRPCExpect{Body: map[string]any{"status": "SERVING"}},
		},
	})
	require.NoError(t, r.Run(context.Background(), s))
}

func TestGRPCStepExpectBodyFieldMismatch(t *testing.T) {
	store := events.NewMemStore(0)
	fake := &fakeGRPCPort{resp: &grpcc.Invocation{Code: 0, Body: map[string]any{"status": "NOT_SERVING"}}}
	r := orchestrator.New(&scenario.Scenario{}, nil, nil, store)
	r.GRPC = fake

	s := grpcScenario(scenario.Step{
		Name: "drift",
		GRPC: &scenario.GRPCStep{
			Proto:   "x",
			Method:  "pkg.Svc/Check",
			Request: `{}`,
			Expect:  &scenario.GRPCExpect{Body: map[string]any{"status": "SERVING"}},
		},
	})
	err := r.Run(context.Background(), s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "body field \"status\"")
}

func TestGRPCStepExpandsInterpolatedRequest(t *testing.T) {
	store := events.NewMemStore(0)
	fake := &fakeGRPCPort{resp: &grpcc.Invocation{Code: 0, Body: map[string]any{"ok": true}}}
	r := orchestrator.New(&scenario.Scenario{}, nil, nil, store)
	r.GRPC = fake
	r.RunID = "run-42"

	s := grpcScenario(scenario.Step{
		Name: "call",
		GRPC: &scenario.GRPCStep{
			Proto:   "x",
			Method:  "pkg.Svc/Do",
			Request: `{"id":"${run.id}"}`,
		},
	})
	require.NoError(t, r.Run(context.Background(), s))
	assert.Contains(t, fake.lastReq.Request, "run-42", "${run.id} must be expanded before Invoke")
}

func TestGRPCStepBubblesPortErrors(t *testing.T) {
	store := events.NewMemStore(0)
	fake := &fakeGRPCPort{err: errors.New("dial failed")}
	r := orchestrator.New(&scenario.Scenario{}, nil, nil, store)
	r.GRPC = fake

	s := grpcScenario(scenario.Step{
		Name: "boom",
		GRPC: &scenario.GRPCStep{Proto: "x", Method: "pkg.Svc/Do", Request: "{}"},
	})
	err := r.Run(context.Background(), s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dial failed")
}

func TestGRPCStepRejectsMissingConnector(t *testing.T) {
	store := events.NewMemStore(0)
	r := orchestrator.New(&scenario.Scenario{}, nil, nil, store)
	r.GRPC = &fakeGRPCPort{}
	s := &scenario.Scenario{Spec: scenario.Spec{Steps: []scenario.Step{
		{Name: "g", GRPC: &scenario.GRPCStep{Proto: "x", Method: "p.S/M", Request: "{}"}},
	}}}
	err := r.Run(context.Background(), s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connectors.grpc")
}

func TestGRPCStepRejectsMissingPort(t *testing.T) {
	store := events.NewMemStore(0)
	r := orchestrator.New(&scenario.Scenario{}, nil, nil, store)
	s := grpcScenario(scenario.Step{Name: "g", GRPC: &scenario.GRPCStep{Proto: "x", Method: "p.S/M", Request: "{}"}})
	err := r.Run(context.Background(), s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no client configured")
}

// Codex P1 2026-04-24: equalAny used to stringify both sides via fmt.Sprint,
// silently equating "5" (string) with 5 (number). Strict compare now refuses.
func TestGRPCExpectBodyStringVsNumberMismatch(t *testing.T) {
	store := events.NewMemStore(0)
	fake := &fakeGRPCPort{resp: &grpcc.Invocation{Code: 0, Body: map[string]any{"count": float64(5)}}}
	r := orchestrator.New(&scenario.Scenario{}, nil, nil, store)
	r.GRPC = fake

	s := grpcScenario(scenario.Step{
		Name: "strict",
		GRPC: &scenario.GRPCStep{
			Proto:   "x",
			Method:  "pkg.Svc/Check",
			Request: `{}`,
			Expect:  &scenario.GRPCExpect{Body: map[string]any{"count": "5"}}, // string vs number
		},
	})
	err := r.Run(context.Background(), s)
	require.Error(t, err, "string 5 must not match number 5")
	assert.Contains(t, err.Error(), "body field \"count\"")
}

func TestGRPCExpectBodyNumericCoercionStillWorks(t *testing.T) {
	store := events.NewMemStore(0)
	fake := &fakeGRPCPort{resp: &grpcc.Invocation{Code: 0, Body: map[string]any{"count": float64(5)}}}
	r := orchestrator.New(&scenario.Scenario{}, nil, nil, store)
	r.GRPC = fake

	s := grpcScenario(scenario.Step{
		Name: "numeric",
		GRPC: &scenario.GRPCStep{
			Proto:   "x",
			Method:  "pkg.Svc/Check",
			Request: `{}`,
			Expect:  &scenario.GRPCExpect{Body: map[string]any{"count": 5}}, // int vs float64
		},
	})
	require.NoError(t, r.Run(context.Background(), s), "numeric types across int/float must still match")
}
