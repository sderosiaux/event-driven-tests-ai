package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/controlplane/storage"
)

// MountEvalRunReads attaches read-only eval-run routes.
func (a *API) MountEvalRunReads(r chi.Router) {
	r.Get("/api/v1/eval-runs", a.listEvalRuns)
	r.Get("/api/v1/eval-runs/{id}", a.getEvalRun)
}

// MountEvalRunWrites attaches ingest routes for `edt eval` reports.
func (a *API) MountEvalRunWrites(r chi.Router) {
	r.Post("/api/v1/eval-runs", a.ingestEvalRun)
}

// evalRunRequest is the wire shape accepted by POST /api/v1/eval-runs.
type evalRunRequest struct {
	ID         string           `json:"id"`
	Scenario   string           `json:"scenario"`
	JudgeModel string           `json:"judge_model,omitempty"`
	Iterations int              `json:"iterations,omitempty"`
	StartedAt  time.Time        `json:"started_at"`
	FinishedAt time.Time        `json:"finished_at"`
	Status     string           `json:"status"`
	Results    []evalResultWire `json:"results"`
}

type evalResultWire struct {
	Name            string  `json:"name"`
	Aggregate       string  `json:"aggregate,omitempty"`
	Samples         int     `json:"samples"`
	RequiredSamples int     `json:"required_samples,omitempty"`
	Value           float64 `json:"value"`
	Threshold       string  `json:"threshold,omitempty"`
	Passed          bool    `json:"passed"`
	Status          string  `json:"status"`
	Errors          int     `json:"errors,omitempty"`
}

func (a *API) ingestEvalRun(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var req evalRunRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid eval-run json: "+err.Error())
		return
	}
	if req.ID == "" || req.Scenario == "" {
		writeError(w, http.StatusBadRequest, "id and scenario are required")
		return
	}
	if req.Status == "" {
		writeError(w, http.StatusBadRequest, "status is required (pass | fail | error)")
		return
	}
	run := storage.EvalRun{
		ID:         req.ID,
		Scenario:   req.Scenario,
		JudgeModel: req.JudgeModel,
		Iterations: req.Iterations,
		StartedAt:  req.StartedAt,
		FinishedAt: req.FinishedAt,
		Status:     req.Status,
	}
	rows := make([]storage.EvalResult, len(req.Results))
	for i, e := range req.Results {
		rows[i] = storage.EvalResult{
			Name:            e.Name,
			Aggregate:       e.Aggregate,
			Samples:         e.Samples,
			RequiredSamples: e.RequiredSamples,
			Value:           e.Value,
			Threshold:       e.Threshold,
			Passed:          e.Passed,
			Status:          e.Status,
			Errors:          e.Errors,
		}
	}
	if err := a.Store.RecordEvalRun(r.Context(), run, rows); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":       run.ID,
		"scenario": run.Scenario,
		"status":   run.Status,
	})
}

func (a *API) getEvalRun(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	run, rows, err := a.Store.GetEvalRun(r.Context(), id)
	if err != nil {
		writeError(w, statusForStorageErr(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"run":     evalRunToWire(run),
		"results": evalRowsToWire(rows),
	})
}

func (a *API) listEvalRuns(w http.ResponseWriter, r *http.Request) {
	scenario := r.URL.Query().Get("scenario")
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	runs, err := a.Store.ListEvalRuns(r.Context(), scenario, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, len(runs))
	for i, run := range runs {
		out[i] = evalRunToWire(run)
	}
	writeJSON(w, http.StatusOK, out)
}

func evalRunToWire(r storage.EvalRun) map[string]any {
	return map[string]any{
		"id":          r.ID,
		"scenario":    r.Scenario,
		"judge_model": r.JudgeModel,
		"iterations":  r.Iterations,
		"started_at":  r.StartedAt,
		"finished_at": r.FinishedAt,
		"status":      r.Status,
	}
}

func evalRowsToWire(rows []storage.EvalResult) []map[string]any {
	out := make([]map[string]any, len(rows))
	for i, r := range rows {
		out[i] = map[string]any{
			"name":             r.Name,
			"aggregate":        r.Aggregate,
			"samples":          r.Samples,
			"required_samples": r.RequiredSamples,
			"value":            r.Value,
			"threshold":        r.Threshold,
			"passed":           r.Passed,
			"status":           r.Status,
			"errors":           r.Errors,
		}
	}
	return out
}
