package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/checks"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/events"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/httpc"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/kafka"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/orchestrator"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/report"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/grpcc"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/reporter"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/ws"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
	sr "github.com/sderosiaux/event-driven-tests-ai/pkg/schemaregistry"
	"github.com/spf13/cobra"
)

type runFlags struct {
	file             string
	format           string // json | console
	bootstrapServers string
	timeout          time.Duration
	reportTo         string // control-plane base URL for PushReport
	reportToken      string // optional bearer token
}

func newRunCmd() *cobra.Command {
	f := &runFlags{}
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run a scenario once (CI mode)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return doRun(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), f)
		},
	}
	cmd.Flags().StringVar(&f.file, "file", "", "Path to scenario YAML (required)")
	cmd.Flags().StringVar(&f.format, "format", "console", "Report format: console | json")
	cmd.Flags().StringVar(&f.bootstrapServers, "bootstrap-servers", "", "Override Kafka bootstrap servers")
	cmd.Flags().DurationVar(&f.timeout, "timeout", 10*time.Minute, "Overall run timeout")
	cmd.Flags().StringVar(&f.reportTo, "report-to", os.Getenv("EDT_CONTROL_PLANE"), "Control plane base URL — pushes the run report on completion")
	cmd.Flags().StringVar(&f.reportToken, "report-token", os.Getenv("EDT_TOKEN"), "Bearer token for --report-to")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func doRun(ctx context.Context, stdout, stderr io.Writer, f *runFlags) error {
	raw, err := os.ReadFile(f.file)
	if err != nil {
		return err
	}
	// Expand ${ENV_VAR} placeholders (bare ALL_CAPS/underscore names only)
	// from the host environment before parsing, so templated scenarios —
	// notably examples/showcase/*.yaml — share one file between docker-compose
	// and local dev with different broker hostnames.
	// Scenario-level interpolations like ${run.id}, ${previous.foo}, ${uuid()},
	// ${faker.person.id()} contain dots or parens and are deliberately NOT
	// env-expanded; they pass through untouched for the orchestrator to handle
	// at step time.
	b := []byte(expandHostEnv(string(raw)))
	s, err := scenario.Parse(b)
	if err != nil {
		return err
	}
	if err := scenario.ValidateYAML(b); err != nil {
		return err
	}

	// Apply CLI overrides.
	if f.bootstrapServers != "" && s.Spec.Connectors.Kafka != nil {
		s.Spec.Connectors.Kafka.BootstrapServers = f.bootstrapServers
	}

	// Signal handling for graceful shutdown (Ctrl-C flushes the report).
	if f.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, f.timeout)
		defer cancel()
	}
	var stopSignals func()
	ctx, stopSignals = withSignalCancel(ctx)
	defer stopSignals()

	runID := newRunID()
	rep := &report.Report{
		Scenario:  s.Metadata.Name,
		RunID:     runID,
		Mode:      "run",
		StartedAt: time.Now().UTC(),
	}

	// Build ports. Each consume step builds its own subscriber on demand,
	// so the long-lived client only owns the producer.
	var kp orchestrator.KafkaPort
	if s.Spec.Connectors.Kafka != nil {
		kc, err := kafka.NewClient(s.Spec.Connectors.Kafka)
		if err != nil {
			rep.Error = err.Error()
			return writeAndExit(stdout, rep, f.format)
		}
		defer kc.Close()
		kp = kc
	}
	var hp orchestrator.HTTPPort
	var ssePort orchestrator.SSEPort
	if s.Spec.Connectors.HTTP != nil {
		cl := httpc.NewClient(s.Spec.Connectors.HTTP)
		hp = cl
		ssePort = httpc.NewSSEAdapter(cl)
	}

	// Orchestrate.
	store := events.NewMemStore(0)
	runner := orchestrator.New(s, kp, hp, store)
	runner.RunID = runID
	if s.Spec.Connectors.WebSocket != nil {
		runner.WebSocket = ws.NewAdapter()
	}
	if ssePort != nil {
		runner.SSE = ssePort
	}
	if s.Spec.Connectors.GRPC != nil {
		gp := grpcc.NewPort()
		defer gp.Close()
		runner.GRPC = gp
	}
	if sc := srClientFor(s); sc != nil {
		runner.Codec = sr.NewCodec(sc)
	}
	if err := runner.Run(ctx, s); err != nil {
		rep.Error = err.Error()
	}

	// Evaluate checks on whatever was collected.
	evalr, err := checks.NewEvaluator(store)
	if err != nil {
		fmt.Fprintf(stderr, "warning: could not build evaluator: %v\n", err)
	} else {
		rep.Checks = checks.EvaluateAll(evalr, s.Spec.Checks)
	}
	rep.EventCount = store.Len()
	rep.Finalize()

	// Push to control plane if requested. Failure here MUST NOT alter the
	// scenario's exit code — losing the control plane must not turn a green
	// CI build red.
	if f.reportTo != "" {
		pushCtx, pushCancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := reporter.New(f.reportTo, f.reportToken).PushReport(pushCtx, rep); err != nil {
			fmt.Fprintf(stderr, "warning: push report to %s failed: %v\n", f.reportTo, err)
		}
		pushCancel()
	}

	return writeAndExit(stdout, rep, f.format)
}

// exitError carries a process exit code through cobra.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }
func (e *exitError) ExitCode() int { return e.code }

// writeAndExit prints the report and returns an error carrying the exit code.
// main.go checks for *exitError and calls os.Exit accordingly, so a scenario
// error yields exit 2 while a critical check failure yields exit 1.
func writeAndExit(w io.Writer, r *report.Report, format string) error {
	switch format {
	case "json":
		if err := report.WriteJSON(w, r); err != nil {
			return err
		}
	default:
		if err := report.WriteConsole(w, r); err != nil {
			return err
		}
	}
	switch r.ExitCode {
	case 0:
		return nil
	case 2:
		return &exitError{code: 2, msg: fmt.Sprintf("scenario error: %s", r.Error)}
	default:
		return &exitError{code: 1, msg: "check failures"}
	}
}

// srClientFor builds a Schema Registry client from the scenario's connector
// config, or nil when none is declared.
func srClientFor(s *scenario.Scenario) *sr.Client {
	if s.Spec.Connectors.Kafka == nil || s.Spec.Connectors.Kafka.SchemaRegistry == nil {
		return nil
	}
	r := s.Spec.Connectors.Kafka.SchemaRegistry
	if r.URL == "" {
		return nil
	}
	base := r.BasePath
	if base == "" && r.Flavor == "apicurio" {
		base = "/apis/ccompat/v6"
	}
	return sr.New(sr.Config{
		URL:      r.URL,
		BasePath: base,
		User:     r.Username,
		Pass:     r.Password,
		BearerTk: r.BearerTk,
	})
}

func newRunID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "r-" + hex.EncodeToString(b[:])
}

// withSignalCancel wraps ctx with cancellation on SIGINT/SIGTERM and returns a
// cleanup function the caller must defer. The cleanup unregisters the signal
// handler and ensures the watcher goroutine exits, even when doRun is called
// expandHostEnv replaces ${NAME} with os.Getenv(NAME) when NAME is a bare
// ALL_CAPS_OR_UNDERSCORE_OR_DIGIT identifier. Any other braced expression
// (with dots, parens, spaces) is left untouched so scenario-level
// interpolations like ${run.id}, ${uuid()}, ${faker.person.id()} pass
// through to the orchestrator and data-generator layers.
func expandHostEnv(s string) string {
	return os.Expand(s, func(key string) string {
		if !isEnvName(key) {
			return "${" + key + "}"
		}
		return os.Getenv(key)
	})
}

func isEnvName(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		switch {
		case r == '_':
		case r >= 'A' && r <= 'Z':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

// in library style with a long-lived parent context.
func withSignalCancel(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-ch:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, func() {
		signal.Stop(ch)
		cancel()
		<-done
	}
}
