package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/event-driven-tests-ai/edt/pkg/checks"
	"github.com/event-driven-tests-ai/edt/pkg/controlplane/storage"
	"github.com/event-driven-tests-ai/edt/pkg/report"
	"github.com/go-chi/chi/v5"
)

// MountRuns attaches the run-ingestion + SLO routes onto a chi router.
func (a *API) MountRuns(r chi.Router) {
	r.Route("/api/v1/runs", func(r chi.Router) {
		r.Post("/", a.ingestRun)
		r.Get("/", a.listRuns)
		r.Get("/{id}", a.getRun)
	})
	r.Get("/api/v1/scenarios/{name}/slo", a.scenarioSLO)
}

// ingestRun consumes a report.Report JSON body and persists it. The report's
// run_id is preserved verbatim so the CLI's identifier is the public name.
func (a *API) ingestRun(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var rep report.Report
	if err := json.Unmarshal(body, &rep); err != nil {
		writeError(w, http.StatusBadRequest, "invalid report json: "+err.Error())
		return
	}
	if rep.Scenario == "" || rep.RunID == "" {
		writeError(w, http.StatusBadRequest, "scenario and run_id are required")
		return
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
	if err := a.Store.RecordRun(r.Context(), run, checkRows); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"run_id":   rep.RunID,
		"scenario": rep.Scenario,
		"status":   rep.Status,
	})
}

func encodeCheckValue(c checks.CheckResult) string {
	if c.Value == nil {
		return ""
	}
	b, err := json.Marshal(c.Value)
	if err != nil {
		return ""
	}
	return string(b)
}

func (a *API) getRun(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	run, checkRows, err := a.Store.GetRun(r.Context(), id)
	if err != nil {
		writeError(w, statusForStorageErr(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"run":    runToWire(run),
		"checks": checkRowsToWire(checkRows),
	})
}

func (a *API) listRuns(w http.ResponseWriter, r *http.Request) {
	scenario := r.URL.Query().Get("scenario")
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	runs, err := a.Store.ListRuns(r.Context(), scenario, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, len(runs))
	for i, run := range runs {
		out[i] = runToWire(run)
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) scenarioSLO(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	window := time.Hour
	if v := r.URL.Query().Get("window"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			window = d
		}
	}
	rates, err := a.Store.SLOPassRate(r.Context(), name, window)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"scenario":   name,
		"window":     window.String(),
		"pass_rates": rates,
	})
}

func runToWire(r storage.Run) map[string]any {
	return map[string]any{
		"id":          r.ID,
		"scenario":    r.Scenario,
		"mode":        r.Mode,
		"status":      r.Status,
		"exit_code":   r.ExitCode,
		"started_at":  r.StartedAt,
		"finished_at": r.FinishedAt,
		"duration_ns": r.Duration,
	}
}

func checkRowsToWire(cs []storage.CheckResult) []map[string]any {
	out := make([]map[string]any, len(cs))
	for i, c := range cs {
		out[i] = map[string]any{
			"name":     c.Name,
			"severity": c.Severity,
			"window":   c.Window,
			"passed":   c.Passed,
			"value":    c.Value,
			"error":    c.Err,
			"at":       c.At,
		}
	}
	return out
}
