package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/checks"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/controlplane/storage"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/events"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/grpcc"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/httpc"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/kafka"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/orchestrator"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/report"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
	sr "github.com/sderosiaux/event-driven-tests-ai/pkg/schemaregistry"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/ws"
)

// MountAdhocRun attaches POST /api/v1/run-adhoc, which executes a scenario
// passed in the body without persisting it as a scenario version. The
// control plane persists the resulting run so the /ui/runs view picks it up
// and the builder can link the caller to the detail view.
//
// This is what powers the builder UI's "Run live" button — no temp files,
// no CLI shell-out, the scenario the user is editing runs in-process and
// the verdict streams back as JSON.
func (a *API) MountAdhocRun(r chi.Router) {
	r.Post("/api/v1/run-adhoc", a.runAdhoc)
}

// adhocTimeout bounds an ad-hoc run so a misbehaving scenario cannot block
// the control plane. The UI's run button is synchronous; 60s is generous
// for realistic "try it now" use and short enough for users to feel
// responsiveness.
const adhocTimeout = 60 * time.Second

func (a *API) runAdhoc(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	yamlBody, err := readScenarioPayload(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	parsed, err := scenario.Parse(yamlBody)
	if err != nil {
		writeError(w, http.StatusBadRequest, "parse: "+err.Error())
		return
	}
	if err := scenario.ValidateYAML(yamlBody); err != nil {
		writeError(w, http.StatusBadRequest, "validation: "+err.Error())
		return
	}
	if parsed.Metadata.Name == "" {
		parsed.Metadata.Name = "adhoc"
	}

	ctx, cancel := context.WithTimeout(r.Context(), adhocTimeout)
	defer cancel()

	rep, runErr := executeAdhoc(ctx, parsed, yamlBody)

	// Persist even failed runs so the user can drill into them from the UI.
	if rep != nil {
		_ = a.storeAdhocRun(r.Context(), rep)
		if a.Metrics != nil {
			a.Metrics.ObserveRun(rep)
		}
	}

	if runErr != nil {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"error":  runErr.Error(),
			"report": rep,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"report": rep})
}

// executeAdhoc wires a full orchestrator.Runner using the scenario's
// connector config and returns the finalized report. Network errors are
// returned alongside the partial report — the caller still gets a useful
// verdict even when one step fails mid-flow.
func executeAdhoc(ctx context.Context, s *scenario.Scenario, rawYAML []byte) (*report.Report, error) {
	rep := &report.Report{
		Scenario:  s.Metadata.Name,
		RunID:     adhocRunID(),
		Mode:      "adhoc",
		StartedAt: time.Now().UTC(),
	}

	store := events.NewMemStore(0)
	var kp orchestrator.KafkaPort
	if s.Spec.Connectors.Kafka != nil {
		kc, err := kafka.NewClient(s.Spec.Connectors.Kafka)
		if err != nil {
			rep.Finalize()
			rep.Error = fmt.Sprintf("kafka client: %v", err)
			return rep, err
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

	runner := orchestrator.New(s, kp, hp, store)
	runner.RunID = rep.RunID
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
	if sc := adhocSRClient(s); sc != nil {
		runner.Codec = sr.NewCodec(sc)
	}

	if err := runner.Run(ctx, s); err != nil {
		rep.Error = err.Error()
		rep.ExitCode = 2
		rep.Finalize()
		// Still evaluate checks against whatever made it into the store so the
		// user sees partial verdicts.
		evalChecks(ctx, s, store, rep)
		return rep, err
	}
	rep.ExitCode = 0
	evalChecks(ctx, s, store, rep)
	rep.Finalize()
	return rep, nil
}

func evalChecks(_ context.Context, s *scenario.Scenario, store events.Store, rep *report.Report) {
	evalr, err := checks.NewEvaluator(store)
	if err != nil {
		rep.Error = "check evaluator: " + err.Error()
		return
	}
	rep.Checks = checks.EvaluateAll(evalr, s.Spec.Checks)
	rep.EventCount = store.Len()
}

func (a *API) storeAdhocRun(ctx context.Context, rep *report.Report) error {
	body, err := json.Marshal(rep)
	if err != nil {
		return err
	}
	run := storage.Run{
		ID:         rep.RunID,
		Scenario:   rep.Scenario,
		Mode:       rep.Mode,
		Status:     string(rep.Status),
		ExitCode:   rep.ExitCode,
		StartedAt:  rep.StartedAt,
		FinishedAt: rep.FinishedAt,
		Duration:   rep.Duration,
		Report:     body,
	}
	checkRows := make([]storage.CheckResult, len(rep.Checks))
	for i, c := range rep.Checks {
		checkRows[i] = storage.CheckResult{
			Name:     c.Name,
			Severity: string(c.Severity),
			Window:   c.Window,
			Passed:   c.Passed,
			Value:    encodeCheckValue(c),
			Err:      c.Err,
			At:       c.At,
		}
	}
	return a.Store.RecordRun(ctx, run, checkRows)
}

func adhocRunID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "adhoc-" + hex.EncodeToString(b[:])
}

func adhocSRClient(s *scenario.Scenario) *sr.Client {
	if s.Spec.Connectors.Kafka == nil || s.Spec.Connectors.Kafka.SchemaRegistry == nil {
		return nil
	}
	c := s.Spec.Connectors.Kafka.SchemaRegistry
	return sr.New(sr.Config{
		URL:      c.URL,
		BasePath: c.BasePath,
		User:     c.Username,
		Pass:     c.Password,
		BearerTk: c.BearerTk,
	})
}
