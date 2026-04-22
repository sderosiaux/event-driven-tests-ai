package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/event-driven-tests-ai/edt/pkg/controlplane/storage"
	"github.com/go-chi/chi/v5"
)

// MountWorkers wires worker registration + heartbeat + assignment endpoints.
func (a *API) MountWorkers(r chi.Router) {
	r.Route("/api/v1/workers", func(r chi.Router) {
		r.Post("/register", a.registerWorker)
		r.Get("/", a.listWorkers)
		r.Post("/{id}/heartbeat", a.heartbeatWorker)
		r.Get("/{id}/assignments", a.listAssignments)
		r.Post("/{id}/assignments", a.assignScenario)
	})
}

type registerRequest struct {
	Labels  map[string]string `json:"labels"`
	Version string            `json:"version"`
}

type registerResponse struct {
	WorkerID string `json:"worker_id"`
	Token    string `json:"token,omitempty"` // populated when M2-T10 wires auth
}

func (a *API) registerWorker(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<16))
	var req registerRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	worker, err := a.Store.RegisterWorker(r.Context(), req.Labels, req.Version)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, registerResponse{WorkerID: worker.ID})
}

type heartbeatResponse struct {
	WorkerID    string                `json:"worker_id"`
	Assignments []assignmentWireShape `json:"assignments"`
}

type assignmentWireShape struct {
	Scenario   string `json:"scenario"`
	AssignedAt string `json:"assigned_at"`
}

func (a *API) heartbeatWorker(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := a.Store.Heartbeat(r.Context(), id); err != nil {
		writeError(w, statusForStorageErr(err), err.Error())
		return
	}
	as, err := a.Store.ListAssignments(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]assignmentWireShape, len(as))
	for i, x := range as {
		out[i] = assignmentWireShape{Scenario: x.ScenarioName, AssignedAt: x.AssignedAt.UTC().Format("2006-01-02T15:04:05Z")}
	}
	writeJSON(w, http.StatusOK, heartbeatResponse{WorkerID: id, Assignments: out})
}

func (a *API) listWorkers(w http.ResponseWriter, r *http.Request) {
	ws, err := a.Store.ListWorkers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, len(ws))
	for i, x := range ws {
		out[i] = map[string]any{
			"id":             x.ID,
			"labels":         x.Labels,
			"version":        x.Version,
			"registered_at":  x.RegisteredAt,
			"last_heartbeat": x.LastHeartbeat,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) listAssignments(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	as, err := a.Store.ListAssignments(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]assignmentWireShape, len(as))
	for i, x := range as {
		out[i] = assignmentWireShape{Scenario: x.ScenarioName, AssignedAt: x.AssignedAt.UTC().Format("2006-01-02T15:04:05Z")}
	}
	writeJSON(w, http.StatusOK, out)
}

type assignRequest struct {
	Scenario string `json:"scenario"`
}

func (a *API) assignScenario(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	body, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<16))
	var req assignRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Scenario == "" {
		writeError(w, http.StatusBadRequest, "scenario is required")
		return
	}
	if err := a.Store.AssignScenario(r.Context(), id, req.Scenario); err != nil {
		writeError(w, statusForStorageErr(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"worker_id": id, "scenario": req.Scenario})
}

// silence unused import lint when storage gains new fields without a wire-shape
// update.
var _ storage.Worker
