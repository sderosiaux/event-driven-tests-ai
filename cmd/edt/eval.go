package main

import (
	cryptoRand "crypto/rand"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/eval"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/events"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/httpc"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/kafka"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/orchestrator"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/reporter"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
	sr "github.com/sderosiaux/event-driven-tests-ai/pkg/schemaregistry"
	"github.com/spf13/cobra"
)

type evalFlags struct {
	file         string
	iterations   int
	matchTimeout time.Duration
	apiKey       string
	model        string
	reportTo     string
	reportToken  string
}

func newEvalCmd() *cobra.Command {
	f := &evalFlags{}
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Run an agent-under-test harness: seed inputs, observe the agent, judge the outputs",
		RunE: func(cmd *cobra.Command, args []string) error {
			if f.file == "" {
				return fmt.Errorf("--file is required")
			}
			if f.apiKey == "" {
				f.apiKey = os.Getenv("ANTHROPIC_API_KEY")
			}
			if f.reportTo == "" {
				f.reportTo = os.Getenv("EDT_CONTROL_PLANE")
			}
			if f.reportToken == "" {
				f.reportToken = os.Getenv("EDT_TOKEN")
			}
			return doEval(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), f)
		},
	}
	cmd.Flags().StringVar(&f.file, "file", "", "Scenario YAML (must include spec.agent_under_test + evals)")
	cmd.Flags().IntVar(&f.iterations, "iterations", 50, "How many pairs to judge before aggregating")
	cmd.Flags().DurationVar(&f.matchTimeout, "match-timeout", 10*time.Second, "Max wait per input for the agent's matching output")
	cmd.Flags().StringVar(&f.apiKey, "anthropic-api-key", "", "Anthropic API key (defaults to ANTHROPIC_API_KEY)")
	cmd.Flags().StringVar(&f.model, "judge-model", "", "Override the judge model (default: scenario's eval.judge.model, fallback claude-opus-4-7)")
	cmd.Flags().StringVar(&f.reportTo, "report-to", "", "Control plane base URL to push the eval report")
	cmd.Flags().StringVar(&f.reportToken, "report-token", "", "Bearer token for --report-to")
	return cmd
}

// doEval is the eval counterpart of doRun. It parses + validates the scenario,
// seeds input events via the orchestrator's produce path, reads back the
// agent's outputs from the store, and hands each pair to the LLM judge.
func doEval(ctx context.Context, stdout, stderr io.Writer, f *evalFlags) error {
	b, err := os.ReadFile(f.file)
	if err != nil {
		return err
	}
	s, err := scenario.Parse(b)
	if err != nil {
		return err
	}
	if err := scenario.ValidateYAML(b); err != nil {
		return err
	}
	if s.Spec.AgentUnderTest == nil {
		return fmt.Errorf("eval: scenario is missing spec.agent_under_test")
	}
	if len(s.Spec.Evals) == 0 {
		return fmt.Errorf("eval: scenario defines no evals block")
	}

	// Build the ambient orchestrator — produce/consume/HTTP share the same
	// connector wiring as `edt run`.
	store := events.NewMemStore(0)
	var kp orchestrator.KafkaPort
	if s.Spec.Connectors.Kafka != nil {
		kc, err := kafka.NewClient(s.Spec.Connectors.Kafka)
		if err != nil {
			return fmt.Errorf("eval: kafka client: %w", err)
		}
		defer kc.Close()
		kp = kc
	}
	var hp orchestrator.HTTPPort
	if s.Spec.Connectors.HTTP != nil {
		hp = httpc.NewClient(s.Spec.Connectors.HTTP)
	}
	runner := orchestrator.New(s, kp, hp, store)
	if sc := srClientFor(s); sc != nil {
		runner.Codec = sr.NewCodec(sc)
	}

	judge := eval.NewLLMJudge(f.apiKey, f.model)
	exec := eval.New(eval.Config{
		Judge:        judge,
		MatchTimeout: f.matchTimeout,
		OnScore: func(name string, sc eval.Score) {
			if sc.Err != nil {
				fmt.Fprintf(stderr, "eval[%s]: judge error: %v\n", name, sc.Err)
				return
			}
			fmt.Fprintf(stderr, "eval[%s]: score=%.2f rationale=%q\n", name, sc.Value, truncateCLI(sc.Rationale, 120))
		},
	})

	// Each iteration reseeds the scenario's produce steps and judges only the
	// events this iteration created. --iterations now controls sample count,
	// not "first N events of a single seeding batch".
	seedOnly := produceOnlyScenario(s)
	startedAt := time.Now().UTC()
	reseed := func(ctx context.Context) error {
		if err := runner.Run(ctx, seedOnly); err != nil {
			return fmt.Errorf("scenario seeding: %w", err)
		}
		return nil
	}
	if err := exec.RunIterations(ctx, s, store, reseed, f.iterations); err != nil {
		return err
	}
	results := exec.Finalize(s)
	finishedAt := time.Now().UTC()
	out := map[string]any{
		"scenario": s.Metadata.Name,
		"mode":     "eval",
		"results":  results,
	}
	buf, _ := json.MarshalIndent(out, "", "  ")
	_, _ = stdout.Write(append(buf, '\n'))

	// Optional control-plane push.
	if f.reportTo != "" {
		if err := pushEvalReport(ctx, f, s, results, startedAt, finishedAt, stderr); err != nil {
			fmt.Fprintf(stderr, "eval: push report failed: %v\n", err)
		}
	}

	// Exit code: non-zero when any eval fails its threshold.
	for _, r := range results {
		if r.Threshold != "" && !r.Passed {
			return &exitError{code: 1, msg: fmt.Sprintf("eval %q below threshold %s (got %.2f)", r.Eval, r.Threshold, r.Value)}
		}
	}
	return nil
}

func produceOnlyScenario(s *scenario.Scenario) *scenario.Scenario {
	if s == nil {
		return nil
	}
	clone := *s
	clone.Spec = s.Spec
	clone.Spec.Steps = nil
	for _, step := range s.Spec.Steps {
		if step.Produce != nil {
			clone.Spec.Steps = append(clone.Spec.Steps, step)
		}
	}
	return &clone
}

func pushEvalReport(ctx context.Context, f *evalFlags, s *scenario.Scenario, results []eval.Result, startedAt, finishedAt time.Time, stderr io.Writer) error {
	client := reporter.New(f.reportTo, f.reportToken)
	perEvalModels := resolvePerEvalModels(s, f.model)
	req := reporter.EvalRunRequest{
		ID:         evalRunID(startedAt),
		Scenario:   s.Metadata.Name,
		JudgeModel: rollUpJudgeModel(perEvalModels),
		Iterations: f.iterations,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		Status:     summariseStatus(results),
		Results:    make([]reporter.EvalResultWire, 0, len(results)),
	}
	for _, r := range results {
		req.Results = append(req.Results, reporter.EvalResultWire{
			Name:            r.Eval,
			JudgeModel:      perEvalModels[r.Eval],
			Aggregate:       r.Aggregate,
			Samples:         r.Over,
			RequiredSamples: r.RequiredOver,
			Value:           r.Value,
			Threshold:       r.Threshold,
			Passed:          r.Passed,
			Status:          r.Status,
			Errors:          r.Errors,
		})
	}
	return client.PushEvalRun(ctx, req)
}

// evalRunID mirrors the format in pkg/reporter.Client.PushReportRaw: a sortable
// timestamp + 8 hex chars of entropy. Keeping the shape stable across the
// transition so operators who indexed on it are not surprised.
func evalRunID(t time.Time) string {
	var b [4]byte
	_, _ = cryptoRand.Read(b[:])
	return "eval-" + t.UTC().Format("20060102T150405") + "-" + hex.EncodeToString(b[:])
}

// resolvePerEvalModels maps each eval.name → the judge model actually used for
// that eval. The CLI --judge-model override wins over everything; otherwise
// each eval's own scenario.judge.model is honoured. Scenarios can legitimately
// mix models (e.g. a cheaper model for coarse checks, Opus for nuance), so
// this must be per-eval, not a single run-wide value.
func resolvePerEvalModels(s *scenario.Scenario, override string) map[string]string {
	out := make(map[string]string, len(s.Spec.Evals))
	for _, ev := range s.Spec.Evals {
		if override != "" {
			out[ev.Name] = override
			continue
		}
		if ev.Judge != nil {
			out[ev.Name] = ev.Judge.Model
		}
	}
	return out
}

// rollUpJudgeModel returns the single model used across the run, or "mixed"
// when the run involved more than one. Empty when no evals declared models.
func rollUpJudgeModel(perEval map[string]string) string {
	seen := ""
	for _, m := range perEval {
		if m == "" {
			continue
		}
		if seen == "" {
			seen = m
			continue
		}
		if m != seen {
			return "mixed"
		}
	}
	return seen
}

// summariseStatus rolls the per-eval statuses into a single run-level verdict:
// any "fail" or errored eval → "fail"; otherwise "pass". Matches the runs.status
// convention used elsewhere in the API.
func summariseStatus(results []eval.Result) string {
	for _, r := range results {
		if !r.Passed {
			return "fail"
		}
	}
	return "pass"
}

func truncateCLI(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
