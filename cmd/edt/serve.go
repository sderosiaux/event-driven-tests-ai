package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/checks"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/controlplane"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/controlplane/storage"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/report"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
	"github.com/spf13/cobra"
)

func newServeCmd() *cobra.Command {
	cfg := controlplane.Config{}
	var seedDir string
	var seedDemo bool
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the edt control plane (REST API + UI + /metrics)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.Logger = func(format string, args ...any) {
				fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", args...)
			}
			if cfg.AdminToken == "" {
				cfg.AdminToken = os.Getenv("EDT_ADMIN_TOKEN")
			}
			if cfg.WorkerToken == "" {
				cfg.WorkerToken = os.Getenv("EDT_WORKER_TOKEN")
			}
			if cfg.DBURL == "" {
				cfg.DBURL = os.Getenv("EDT_DB_URL")
			}
			if seedDir == "" {
				seedDir = os.Getenv("EDT_SEED_DIR")
			}
			s, err := controlplane.NewServerAuto(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			if seedDir != "" {
				if err := seedScenarios(cmd.Context(), s.Store(), seedDir, cfg.Logger); err != nil {
					return fmt.Errorf("seed: %w", err)
				}
			}
			if !seedDemo && os.Getenv("EDT_SEED_DEMO") == "1" {
				seedDemo = true
			}
			if seedDemo {
				if err := seedDemoData(cmd.Context(), s.Store(), cfg.Logger); err != nil {
					return fmt.Errorf("seed-demo: %w", err)
				}
			}
			return s.Run(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&cfg.Addr, "addr", ":8080", "Listen address")
	cmd.Flags().StringVar(&cfg.DBURL, "db-url", "", "Postgres connection string (empty = in-memory dev mode)")
	cmd.Flags().BoolVar(&cfg.RequireAuth, "require-auth", false, "Require a valid bearer token on mutating endpoints")
	cmd.Flags().StringVar(&cfg.AdminToken, "admin-token", "", "Bootstrap admin token (also via EDT_ADMIN_TOKEN env)")
	cmd.Flags().StringVar(&cfg.WorkerToken, "worker-token", "", "Bootstrap worker-scoped token (also via EDT_WORKER_TOKEN env)")
	cmd.Flags().StringVar(&seedDir, "seed-dir", "", "Directory of scenario YAML files to upsert on startup (also via EDT_SEED_DIR env)")
	cmd.Flags().BoolVar(&seedDemo, "seed-demo", false, "Seed a demo worker, sample runs, and eval runs so the UI is populated on first boot (also via EDT_SEED_DEMO=1)")
	return cmd
}

// seedScenarios upserts every *.yaml file in dir. Used by the demo stack to
// pre-populate the control plane with showcase scenarios on boot. Files with
// ${ENV_VAR} placeholders in connector URLs are expanded from os.Environ so
// a single template works across docker-compose, local, and CI.
func seedScenarios(ctx context.Context, store storage.Storage, dir string, log func(string, ...any)) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		expanded := []byte(expandHostEnv(string(raw)))
		parsed, err := scenario.Parse(expanded)
		if err != nil {
			log("seed: skip %s: %v", e.Name(), err)
			continue
		}
		if parsed.Metadata.Name == "" {
			log("seed: skip %s: missing metadata.name", e.Name())
			continue
		}
		if _, err := store.UpsertScenario(ctx, parsed.Metadata.Name, expanded, parsed.Metadata.Labels); err != nil {
			return fmt.Errorf("upsert %s: %w", e.Name(), err)
		}
		count++
		log("seed: upserted %s from %s", parsed.Metadata.Name, e.Name())
	}
	log("seed: %d scenarios loaded from %s", count, dir)
	return nil
}

// seedDemoData populates the in-memory store with a demo worker, a few
// realistic Run rows (mix of pass/fail) and a couple of EvalRun rows.
// Used by --seed-demo so the UI shows live-looking data on first boot
// without requiring a real Kafka stack or worker process.
//
// The data is synthesised — no scenario actually executed — but the
// shapes round-trip through the same Store API real workers use, so the
// Runs / Evals / Workers screens render exactly as they would in prod.
func seedDemoData(ctx context.Context, store storage.Storage, log func(string, ...any)) error {
	w, err := store.RegisterWorker(ctx, map[string]string{
		"env":    "demo",
		"region": "local",
	}, "v0.1.0-demo")
	if err != nil {
		return fmt.Errorf("worker: %w", err)
	}
	if err := store.Heartbeat(ctx, w.ID); err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}

	scns, err := store.ListScenarios(ctx)
	if err != nil {
		return fmt.Errorf("list scenarios: %w", err)
	}
	if len(scns) == 0 {
		log("seed-demo: no scenarios found, skipping run/eval seed")
		return nil
	}

	now := time.Now().UTC()
	runs := 0
	evals := 0
	for i, s := range scns {
		hasEvals := false
		if parsed, perr := scenario.Parse(s.YAML); perr == nil && len(parsed.Spec.Evals) > 0 {
			hasEvals = true
		}
		// Skip every third scenario to leave realistic gaps in the run list,
		// EXCEPT scenarios that define evals — those are the most interesting
		// to surface in the demo, so always include them.
		if !hasEvals && i%3 == 2 {
			continue
		}
		var status report.Status
		switch {
		case i%5 == 4:
			status = report.StatusError
		case i%2 == 0:
			status = report.StatusPass
		default:
			status = report.StatusFail
		}
		ago := time.Duration(15+i*7) * time.Minute
		started := now.Add(-ago)
		duration := time.Duration(180+i*40) * time.Millisecond
		if err := recordDemoRun(ctx, store, s, status, started, duration, w.ID); err != nil {
			return fmt.Errorf("run %s: %w", s.Name, err)
		}
		runs++

		// Eval run ONLY when the scenario actually defines evals. Seeding
		// fillers for scenarios without an evals block was confusing —
		// users opened those rows and saw no rubrics in the detail page
		// because the source scenario simply doesn't have any.
		if hasEvals {
			if err := recordDemoEvalRun(ctx, store, s, started.Add(time.Minute), status); err != nil {
				return fmt.Errorf("eval %s: %w", s.Name, err)
			}
			evals++
		}
	}
	log("seed-demo: 1 worker, %d runs, %d eval runs", runs, evals)
	return nil
}

func recordDemoRun(ctx context.Context, store storage.Storage, s storage.Scenario, status report.Status, started time.Time, dur time.Duration, workerID string) error {
	parsed, err := scenario.Parse(s.YAML)
	if err != nil {
		return err
	}
	finished := started.Add(dur)
	id := "demo-" + randHex(8)

	// Synthesise check results from the parsed scenario's checks. First check
	// fails when the run is fail/error; the rest pass.
	checkResults := make([]checks.CheckResult, 0, len(parsed.Spec.Checks))
	storeChecks := make([]storage.CheckResult, 0, len(parsed.Spec.Checks))
	for ci, c := range parsed.Spec.Checks {
		passed := !(ci == 0 && status != report.StatusPass)
		errMsg := ""
		if !passed && status == report.StatusError {
			errMsg = "stream evaluation timed out after 60s"
		}
		valueRaw := "true"
		if !passed {
			valueRaw = `"ratio=0.23"`
		}
		checkResults = append(checkResults, checks.CheckResult{
			Name: c.Name, Expr: c.Expr, Severity: c.Severity, Window: c.Window,
			Passed: passed, Value: passed, Err: errMsg, At: finished,
		})
		storeChecks = append(storeChecks, storage.CheckResult{
			RunID: id, Name: c.Name, Severity: string(c.Severity), Window: c.Window,
			Passed: passed, Value: valueRaw, Err: errMsg, At: finished,
		})
	}

	rep := report.Report{
		Scenario: s.Name, RunID: id, Mode: "run",
		StartedAt: started, FinishedAt: finished, Duration: dur,
		Status: status, ExitCode: 0, Checks: checkResults,
		EventCount: 250 + len(parsed.Spec.Steps)*73,
	}
	if status == report.StatusFail {
		rep.ExitCode = 1
	} else if status == report.StatusError {
		rep.ExitCode = 2
		rep.Error = "kafka: dial tcp localhost:19092: connection refused (synthetic demo)"
	}
	blob, _ := json.Marshal(rep)

	run := storage.Run{
		ID: id, Scenario: s.Name, Mode: "run",
		Status: string(status), ExitCode: rep.ExitCode,
		StartedAt: started, FinishedAt: finished, Duration: dur,
		Report: blob,
	}
	return store.RecordRun(ctx, run, storeChecks)
}

func recordDemoEvalRun(ctx context.Context, store storage.Storage, s storage.Scenario, started time.Time, status report.Status) error {
	id := "eval-" + randHex(8)
	judge := "claude-sonnet-4-6"
	iterations := 5
	er := storage.EvalRun{
		ID: id, Scenario: s.Name,
		JudgeModel: judge,
		Iterations: iterations,
		StartedAt:  started,
		FinishedAt: started.Add(2 * time.Second),
		Status:     string(status),
	}

	// Prefer the scenario's actual eval names + thresholds when defined; this
	// makes the detail page meaningful (rubric, agent topology, judge model
	// all line up). Fall back to generic placeholder evals otherwise.
	var results []storage.EvalResult
	if parsed, err := scenario.Parse(s.YAML); err == nil && len(parsed.Spec.Evals) > 0 {
		for ei, ev := range parsed.Spec.Evals {
			model := judge
			if ev.Judge != nil && ev.Judge.Model != "" {
				model = ev.Judge.Model
			}
			thr := ""
			agg := "mean"
			if ev.Threshold != nil {
				thr = ev.Threshold.Value
				if ev.Threshold.Aggregate != "" {
					agg = ev.Threshold.Aggregate
				}
			}
			passed := !(ei == 0 && status != report.StatusPass)
			val := 4.4
			if !passed {
				val = 3.1
			}
			st := "pass"
			if !passed {
				st = "fail"
			}
			samplesBlob := synthDemoTranscripts(ev.Name, model, iterations, passed)
			results = append(results, storage.EvalResult{
				RunID: id, Name: ev.Name, JudgeModel: model,
				Aggregate: agg, Samples: iterations, RequiredSamples: iterations,
				Value: val, Threshold: thr, Passed: passed, Status: st,
				SamplesJSON: samplesBlob,
			})
		}
	} else {
		results = []storage.EvalResult{
			{RunID: id, Name: "groundedness", JudgeModel: judge, Aggregate: "mean", Samples: iterations, RequiredSamples: iterations, Value: 0.86, Threshold: ">= 0.8", Passed: true, Status: "pass"},
			{RunID: id, Name: "answer_relevance", JudgeModel: judge, Aggregate: "mean", Samples: iterations, RequiredSamples: iterations, Value: 0.71, Threshold: ">= 0.75", Passed: status == report.StatusPass, Status: string(status)},
		}
	}
	return store.RecordEvalRun(ctx, er, results)
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// synthDemoTranscripts builds N (input, output, score, reasoning) tuples
// that match what an LLM judge would produce against a support-summarizer
// agent. Used only by --seed-demo so the UI's eval detail page has real
// transcripts to render even without a live executor. The shape mirrors
// what a real eval persistor would write, so the same UI code paths
// handle both demo and prod data.
func synthDemoTranscripts(evalName, judgeModel string, n int, overallPassed bool) []byte {
	type sample struct {
		Iteration  int     `json:"iteration"`
		Input      string  `json:"input"`
		Output     string  `json:"output"`
		Score      float64 `json:"score"`
		Reasoning  string  `json:"reasoning"`
		JudgeModel string  `json:"judge_model"`
		LatencyMs  int     `json:"latency_ms"`
	}
	tickets := []struct{ in, out string }{
		{
			"Subject: Cannot log in after password reset\nBody: Hi, I reset my password yesterday and now the app loops back to the login screen. Cleared cache, tried mobile + desktop. Still stuck. iOS 17.4 / Chrome on macOS. Account email is alice@example.com.",
			"User reports a login loop after password reset on both mobile (iOS 17.4) and desktop (Chrome/macOS). Cache cleared, no improvement. Affected account: alice@example.com.",
		},
		{
			"Subject: Invoice INV-3091 has wrong VAT rate\nBody: Our invoice for March (INV-3091) charged 21% VAT but we're a Dutch company so it should be 21% only on intra-EU goods, not services. Can you re-issue it as 0% reverse-charge?",
			"Customer (Dutch company) requests reissue of invoice INV-3091 with 0% reverse-charge VAT instead of 21%, as the original charge was for services and incorrectly applied as goods VAT.",
		},
		{
			"Subject: Bulk export keeps timing out\nBody: Trying to export 18 months of transactions for tax filing. The job runs for ~12 minutes then 504s. Tried smaller windows (3mo) and those work. Is there a hard limit?",
			"Bulk transaction export over 18 months consistently 504s after ~12 minutes. Smaller 3-month exports succeed. Customer asks if there's a hard server-side limit.",
		},
		{
			"Subject: Webhook signatures don't verify\nBody: We migrated our webhook receiver to v2 last week. Signatures from your end now fail HMAC verify. We're using the v2 secret from the dashboard, sha256-base64. Ideas?",
			"Integration partner reports webhook v2 HMAC signatures failing verification despite using the v2 secret from the dashboard (sha256-base64).",
		},
		{
			"Subject: Confused about plan limits\nBody: Hey — the plan page says we have 50K events/mo on Growth but our usage page shows 'over limit' at 38K. Which one's authoritative?",
			"Customer on Growth plan sees 'over limit' warning at 38K events/mo though plan page advertises 50K. Asking which display is authoritative.",
		},
	}
	out := make([]sample, 0, n)
	for i := 0; i < n; i++ {
		t := tickets[i%len(tickets)]
		// The first sample carries the failure when overall failed; remaining
		// samples score above. Conciseness inverts so we get a realistic mix.
		highScore := 5
		lowScore := 2
		if evalName == "conciseness" {
			highScore = 4
			lowScore = 3
		}
		score := float64(highScore)
		reason := "Output covers every key fact (account email, OS/browser, repro steps) without inventing details."
		if !overallPassed && i == 0 {
			score = float64(lowScore)
			if evalName == "faithfulness" {
				reason = "Output adds 'iOS 17.4' but original ticket said 'iOS 17'. Minor fabrication of patch version — fails strict faithfulness."
			} else {
				reason = "Output is roughly the same length as the input — no real compression."
			}
		}
		out = append(out, sample{
			Iteration: i, Input: t.in, Output: t.out,
			Score: score, Reasoning: reason,
			JudgeModel: judgeModel,
			LatencyMs:  420 + i*37,
		})
	}
	blob, _ := json.Marshal(out)
	return blob
}
