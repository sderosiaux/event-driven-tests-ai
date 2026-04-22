package worker

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Loop is the long-running worker process. Use NewLoop + Run to drive it.
//
// Lifecycle on Run:
//  1. Register with the control plane (one-shot).
//  2. Tick: heartbeat, observe assigned scenarios diff, dispatch new ones.
//  3. Each assignment runs in its own goroutine until cancellation.
//
// HandleAssignment is the integration point for the run executor (the M2-T7
// watch-mode runner; until then a one-shot run-mode executor is fine).
type Loop struct {
	Client            *Client
	Labels            map[string]string
	Version           string
	HeartbeatInterval time.Duration
	MaxConcurrentRuns int // bounds simultaneous assignments; defaults to 16
	HandleAssignment  func(ctx context.Context, scenario string, yaml []byte) error
	OnError           func(err error) // optional logger

	id         string
	mu         sync.Mutex
	dispatched map[string]context.CancelFunc // scenario → cancel for its goroutine
	slots      chan struct{}                 // semaphore limiting concurrent runs
}

func NewLoop(c *Client) *Loop {
	return &Loop{
		Client:            c,
		HeartbeatInterval: 10 * time.Second,
		MaxConcurrentRuns: 16,
		OnError:           func(error) {},
		dispatched:        make(map[string]context.CancelFunc),
	}
}

// Run blocks until ctx is cancelled. It returns the registration error or any
// error returned from the heartbeat path that the caller may want to log.
func (l *Loop) Run(ctx context.Context) error {
	resp, err := l.Client.Register(ctx, l.Labels, l.Version)
	if err != nil {
		return fmt.Errorf("worker: register: %w", err)
	}
	l.id = resp.WorkerID

	if l.MaxConcurrentRuns <= 0 {
		l.MaxConcurrentRuns = 16
	}
	l.slots = make(chan struct{}, l.MaxConcurrentRuns)

	tick := time.NewTicker(l.HeartbeatInterval)
	defer tick.Stop()

	// Trigger a heartbeat immediately so a freshly-registered worker picks
	// up its assignments without waiting one tick.
	l.tickOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			l.cancelAllAssignments()
			return nil
		case <-tick.C:
			l.tickOnce(ctx)
		}
	}
}

// ID returns the worker id once registered. Empty before Run.
func (l *Loop) ID() string { return l.id }

func (l *Loop) tickOnce(ctx context.Context) {
	hbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := l.Client.Heartbeat(hbCtx, l.id)
	if err != nil {
		l.OnError(fmt.Errorf("heartbeat: %w", err))
		return
	}
	wanted := make(map[string]struct{}, len(resp.Assignments))
	for _, a := range resp.Assignments {
		wanted[a.Scenario] = struct{}{}
		if !l.isDispatched(a.Scenario) {
			l.dispatch(ctx, a.Scenario)
		}
	}
	l.cancelAssignmentsNotIn(wanted)
}

func (l *Loop) isDispatched(scenario string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.dispatched[scenario]
	return ok
}

func (l *Loop) dispatch(parent context.Context, scenario string) {
	if l.HandleAssignment == nil {
		l.OnError(fmt.Errorf("worker: HandleAssignment is nil; refusing to dispatch %q", scenario))
		return
	}
	yaml, err := l.Client.FetchScenario(parent, scenario)
	if err != nil {
		l.OnError(fmt.Errorf("fetch %q: %w", scenario, err))
		return
	}
	ctx, cancel := context.WithCancel(parent)
	l.mu.Lock()
	l.dispatched[scenario] = cancel
	l.mu.Unlock()

	go func() {
		// Bound concurrent runs: block until a slot is free or ctx cancels.
		select {
		case l.slots <- struct{}{}:
			defer func() { <-l.slots }()
		case <-ctx.Done():
			l.mu.Lock()
			delete(l.dispatched, scenario)
			l.mu.Unlock()
			return
		}
		defer func() {
			l.mu.Lock()
			delete(l.dispatched, scenario)
			l.mu.Unlock()
		}()
		if err := l.HandleAssignment(ctx, scenario, yaml); err != nil {
			l.OnError(fmt.Errorf("assignment %q: %w", scenario, err))
		}
	}()
}

func (l *Loop) cancelAssignmentsNotIn(wanted map[string]struct{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for name, cancel := range l.dispatched {
		if _, keep := wanted[name]; !keep {
			cancel()
			delete(l.dispatched, name)
		}
	}
}

func (l *Loop) cancelAllAssignments() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for name, cancel := range l.dispatched {
		cancel()
		delete(l.dispatched, name)
	}
}
