package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/event-driven-tests-ai/edt/pkg/events"
	"github.com/event-driven-tests-ai/edt/pkg/httpc"
	"github.com/event-driven-tests-ai/edt/pkg/kafka"
	"github.com/event-driven-tests-ai/edt/pkg/orchestrator"
	"github.com/event-driven-tests-ai/edt/pkg/report"
	"github.com/event-driven-tests-ai/edt/pkg/reporter"
	"github.com/event-driven-tests-ai/edt/pkg/scenario"
	"github.com/event-driven-tests-ai/edt/pkg/worker"
	"github.com/spf13/cobra"
)

type workerFlags struct {
	controlPlane string
	token        string
	labels       []string
	hbInterval   time.Duration
	loopInterval time.Duration
	evalInterval time.Duration
	version      string
}

func newWorkerCmd() *cobra.Command {
	f := &workerFlags{}
	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Run a long-lived worker that picks up scenarios assigned by the control plane",
		RunE: func(cmd *cobra.Command, args []string) error {
			if f.controlPlane == "" {
				return fmt.Errorf("--control-plane is required")
			}
			labelMap := parseKVPairs(f.labels)

			cli := worker.NewClient(f.controlPlane, f.token)
			loop := worker.NewLoop(cli)
			loop.Labels = labelMap
			loop.Version = f.version
			loop.HeartbeatInterval = f.hbInterval

			pushClient := reporter.New(f.controlPlane, f.token)
			stderr := cmd.ErrOrStderr()
			loop.OnError = func(err error) {
				fmt.Fprintf(stderr, "worker: %v\n", err)
			}
			wcli := worker.NewClient(f.controlPlane, f.token)
			loop.HandleAssignment = func(ctx context.Context, name string, yamlBody []byte) error {
				return runWatchAssignment(ctx, name, yamlBody, wcli, pushClient, stderr, f.loopInterval, f.evalInterval)
			}

			return loop.Run(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&f.controlPlane, "control-plane", "", "Control plane base URL (required)")
	cmd.Flags().StringVar(&f.token, "token", "", "Bearer token for control plane auth")
	cmd.Flags().StringSliceVar(&f.labels, "labels", nil, "Worker labels (key=value, repeatable)")
	cmd.Flags().DurationVar(&f.hbInterval, "heartbeat-interval", 10*time.Second, "Heartbeat cadence")
	cmd.Flags().DurationVar(&f.loopInterval, "loop-interval", time.Second, "Floor between two scenario step iterations in watch mode")
	cmd.Flags().DurationVar(&f.evalInterval, "report-interval", 30*time.Second, "How often watch mode evaluates checks and pushes a snapshot report")
	cmd.Flags().StringVar(&f.version, "version", version, "Worker version reported on register")
	return cmd
}

// runWatchAssignment runs the scenario in watch mode and pushes a snapshot
// report on every WatchConfig.EvalInterval tick. Before starting Watch, it
// asks the control plane for recent check samples and seeds the orchestrator
// so the first window is not cold after a worker restart.
func runWatchAssignment(
	ctx context.Context, name string, yamlBody []byte,
	wcli *worker.Client,
	push *reporter.Client, stderr interface{ Write([]byte) (int, error) },
	loopInterval, evalInterval time.Duration,
) error {
	s, err := scenario.Parse(yamlBody)
	if err != nil {
		return fmt.Errorf("parse %q: %w", name, err)
	}

	store := events.NewMemStore(0)
	// Best-effort resume: fetch a window of prior samples. Failure here is
	// logged and ignored — a cold start is still correct, just a warmer one
	// is nicer.
	seedCtx, seedCancel := context.WithTimeout(ctx, 5*time.Second)
	if samples, serr := wcli.FetchScenarioState(seedCtx, name, 2*evalInterval+loopInterval); serr == nil {
		for _, sample := range samples {
			store.Append(events.Event{
				Stream:    "check:" + sample.Check,
				Key:       sample.Check,
				Ts:        sample.Ts,
				Payload:   map[string]any{"passed": sample.Passed, "value": sample.Value},
				Direction: events.HTTPCall, // reuse HTTPCall as the generic observed-event direction
			})
		}
	} else {
		fmt.Fprintf(stderr, "worker: resume seed for %s failed: %v\n", name, serr)
	}
	seedCancel()
	var kp orchestrator.KafkaPort
	if s.Spec.Connectors.Kafka != nil {
		kc, err := kafka.NewClient(s.Spec.Connectors.Kafka)
		if err != nil {
			return fmt.Errorf("kafka client %q: %w", name, err)
		}
		defer kc.Close()
		kp = kc
	}
	var hp orchestrator.HTTPPort
	if s.Spec.Connectors.HTTP != nil {
		hp = httpc.NewClient(s.Spec.Connectors.HTTP)
	}

	runID := newRunID()
	runner := orchestrator.New(s, kp, hp, store)
	runner.RunID = runID

	// Silence unused-package warning on report.Report import path.
	_ = report.StatusPass

	cfg := orchestrator.WatchConfig{
		LoopInterval: loopInterval,
		EvalInterval: evalInterval,
		OnReport: func(rep *report.Report) {
			pushCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := push.PushReport(pushCtx, rep); err != nil {
				fmt.Fprintf(stderr, "worker: push report %s: %v\n", rep.RunID, err)
			}
		},
	}
	return runner.Watch(ctx, s, cfg)
}

func parseKVPairs(in []string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for _, raw := range in {
		k, v, ok := strings.Cut(raw, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out
}
