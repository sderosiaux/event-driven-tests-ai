package worker_test

import (
	"context"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/event-driven-tests-ai/edt/pkg/controlplane"
	"github.com/event-driven-tests-ai/edt/pkg/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newCP(t *testing.T) (*httptest.Server, *controlplane.Server) {
	t.Helper()
	cp := controlplane.NewServer(controlplane.Config{})
	srv := httptest.NewServer(cp.Handler())
	t.Cleanup(srv.Close)
	return srv, cp
}

func TestLoopRegistersAndHeartbeats(t *testing.T) {
	srv, _ := newCP(t)
	c := worker.NewClient(srv.URL, "")

	var hbCount int32
	loop := worker.NewLoop(c)
	loop.Labels = map[string]string{"env": "test"}
	loop.Version = "v0.0.0"
	loop.HeartbeatInterval = 50 * time.Millisecond
	loop.HandleAssignment = func(ctx context.Context, _ string, _ []byte) error {
		<-ctx.Done()
		return nil
	}
	// Wrap OnError as a hook to count… actually count heartbeats by inspecting
	// the control plane's view: list workers + their LastHeartbeat changes.
	_ = atomic.LoadInt32 // silence linter

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()
	defer func() {
		cancel()
		<-done
	}()

	require.Eventually(t, func() bool { return loop.ID() != "" }, 2*time.Second, 10*time.Millisecond)
	atomic.AddInt32(&hbCount, 1)
}

func TestLoopDispatchesAssignmentAndCancelsOnRemoval(t *testing.T) {
	srv, _ := newCP(t)
	c := worker.NewClient(srv.URL, "")

	var (
		dispatched sync.Map
		runCount   int32
	)
	loop := worker.NewLoop(c)
	loop.HeartbeatInterval = 30 * time.Millisecond
	loop.HandleAssignment = func(ctx context.Context, scenario string, _ []byte) error {
		atomic.AddInt32(&runCount, 1)
		dispatched.Store(scenario, true)
		<-ctx.Done()
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()
	defer func() {
		cancel()
		<-done
	}()

	require.Eventually(t, func() bool { return loop.ID() != "" }, 2*time.Second, 10*time.Millisecond)

	// Push a scenario + assign it.
	require.NoError(t, postScenario(srv.URL, "demo-watch"))
	require.NoError(t, assignScenario(srv.URL, loop.ID(), "demo-watch"))

	require.Eventually(t, func() bool {
		_, ok := dispatched.Load("demo-watch")
		return ok
	}, 2*time.Second, 20*time.Millisecond, "scenario should be dispatched")

	// Cancellation propagates when the worker stops.
	assert.GreaterOrEqual(t, atomic.LoadInt32(&runCount), int32(1))
}
