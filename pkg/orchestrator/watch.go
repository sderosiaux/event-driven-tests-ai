package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/checks"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/events"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/report"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
)

// WatchConfig parameterises Watch.
type WatchConfig struct {
	// LoopInterval is the minimum gap between two iterations of the scenario
	// step list. A scenario whose steps are themselves blocking (long-lived
	// consume) will iterate slower than this — this is a *floor*, not a cap.
	LoopInterval time.Duration

	// EvalInterval is how often watch-mode checks are evaluated and a snapshot
	// report is emitted. Must be >= 1s.
	EvalInterval time.Duration

	// DefaultWindow is the sliding window applied to checks that omit a
	// `window:` field. Per spec §7.2 the window is part of the check; this
	// is the safety net for under-specified scenarios.
	DefaultWindow time.Duration

	// OnReport is invoked with each finalized snapshot report. It must not
	// block the watcher; do the I/O on a separate goroutine if needed.
	OnReport func(*report.Report)
}

func (c *WatchConfig) defaults() {
	if c.LoopInterval <= 0 {
		c.LoopInterval = time.Second
	}
	if c.EvalInterval <= 0 {
		c.EvalInterval = 30 * time.Second
	}
	if c.DefaultWindow <= 0 {
		c.DefaultWindow = 5 * time.Minute
	}
	if c.OnReport == nil {
		c.OnReport = func(*report.Report) {}
	}
}

// Watch runs the scenario in continuous (watch) mode until ctx is cancelled.
// Steps loop forever; checks are evaluated on cfg.EvalInterval against a
// time-windowed view of the events store.
//
// The same Runner powers both run and watch — Run() is one iteration of the
// step list. Watch wraps it.
func (r *Runner) Watch(ctx context.Context, s *scenario.Scenario, cfg WatchConfig) error {
	cfg.defaults()
	scenarioName := s.Metadata.Name

	stepLoopErrCh := make(chan error, 1)
	loopCtx, loopCancel := context.WithCancel(ctx)
	defer loopCancel()

	go func() {
		stepLoopErrCh <- r.runScenarioLoop(loopCtx, s, cfg.LoopInterval)
	}()

	tick := time.NewTicker(cfg.EvalInterval)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			loopCancel()
			<-stepLoopErrCh
			r.emitWatchSnapshot(scenarioName, s, cfg) // final snapshot
			return nil
		case err := <-stepLoopErrCh:
			// A panic-free step loop exit is an error condition (the scenario
			// could not iterate). Surface it but emit a snapshot first.
			r.emitWatchSnapshot(scenarioName, s, cfg)
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("watch: step loop terminated: %w", err)
			}
			return nil
		case <-tick.C:
			r.emitWatchSnapshot(scenarioName, s, cfg)
		}
	}
}

// runScenarioLoop iterates the scenario steps until ctx is cancelled. Errors
// from individual iterations are swallowed (a watch-mode scenario must keep
// running through transient failures); they are visible to checks because the
// orchestrator records a ProducedFailed/HTTP error event in the store.
func (r *Runner) runScenarioLoop(ctx context.Context, s *scenario.Scenario, minGap time.Duration) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		_ = r.Run(ctx, s) // intentional: errors are recorded as events
		select {
		case <-time.After(minGap):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// emitWatchSnapshot evaluates checks against the windowed view and ships a
// finalized report.Report through cfg.OnReport.
func (r *Runner) emitWatchSnapshot(scenarioName string, s *scenario.Scenario, cfg WatchConfig) {
	// Build a per-check window override — checks specifying their own window
	// are honored individually below.
	defaultSince := time.Now().Add(-cfg.DefaultWindow)
	view := events.WindowedView(r.Store, defaultSince)
	evalr, err := checks.NewEvaluator(view)
	if err != nil {
		return
	}
	results := checks.EvaluateAll(evalr, s.Spec.Checks)

	// Per-check window overrides: re-evaluate any check whose `window` parses
	// to something other than the default. This trades a little extra CEL eval
	// for honouring scenario-author intent.
	for i, c := range s.Spec.Checks {
		d, err := time.ParseDuration(c.Window)
		if err != nil || d == cfg.DefaultWindow {
			continue
		}
		v := events.WindowedView(r.Store, time.Now().Add(-d))
		ev, err := checks.NewEvaluator(v)
		if err != nil {
			continue
		}
		results[i] = checks.EvaluateAll(ev, []scenario.Check{c})[0]
	}

	rep := &report.Report{
		Scenario:   scenarioName,
		RunID:      r.RunID,
		Mode:       "watch",
		StartedAt:  time.Now().UTC().Add(-cfg.EvalInterval),
		Checks:     results,
		EventCount: r.Store.Len(),
	}
	if rep.RunID == "" {
		rep.RunID = newWatchRunID()
	}
	rep.Finalize()
	cfg.OnReport(rep)
}

func newWatchRunID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "w-" + hex.EncodeToString(b[:])
}
