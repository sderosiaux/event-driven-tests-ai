package api

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/event-driven-tests-ai/edt/pkg/controlplane/storage"
	"github.com/event-driven-tests-ai/edt/pkg/scenario"
	"github.com/go-chi/chi/v5"
)

// scenarioResponse is the wire shape returned to API clients.
type scenarioResponse struct {
	Name      string            `json:"name"`
	Version   int               `json:"version"`
	YAML      string            `json:"yaml"`
	Labels    map[string]string `json:"labels,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

func toScenarioResponse(s storage.Scenario) scenarioResponse {
	return scenarioResponse{
		Name: s.Name, Version: s.Version, YAML: string(s.YAML),
		Labels: s.Labels, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
}

// MountScenarioReads attaches read-only scenario routes onto a chi router.
func (a *API) MountScenarioReads(r chi.Router) {
	r.Get("/api/v1/scenarios", a.listScenarios)
	r.Get("/api/v1/scenarios/{name}", a.getScenario)
}

// MountScenarioWrites attaches mutating scenario routes onto a chi router.
func (a *API) MountScenarioWrites(r chi.Router) {
	r.Post("/api/v1/scenarios", a.createScenario)
	r.Put("/api/v1/scenarios/{name}", a.updateScenario)
}

func (a *API) listScenarios(w http.ResponseWriter, r *http.Request) {
	ss, err := a.Store.ListScenarios(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]scenarioResponse, len(ss))
	for i, s := range ss {
		out[i] = toScenarioResponse(s)
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) getScenario(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	s, err := a.Store.GetScenario(r.Context(), name)
	if err != nil {
		writeError(w, statusForStorageErr(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toScenarioResponse(s))
}

// scenarioRequest is what clients send. The body MUST be the canonical YAML
// document; metadata.name in the YAML is the source of truth for the resource
// name on POST. On PUT, the path-param name wins.
type scenarioRequest struct {
	YAML string `json:"yaml"`
}

func (a *API) createScenario(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	yamlBytes, err := readScenarioPayload(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	parsed, err := scenario.Parse(yamlBytes)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid scenario: "+err.Error())
		return
	}
	if err := scenario.ValidateYAML(yamlBytes); err != nil {
		writeError(w, http.StatusBadRequest, "schema validation: "+err.Error())
		return
	}
	if parsed.Metadata.Name == "" {
		writeError(w, http.StatusBadRequest, "metadata.name is required")
		return
	}
	saved, err := a.Store.UpsertScenario(r.Context(), parsed.Metadata.Name, yamlBytes, parsed.Metadata.Labels)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, toScenarioResponse(saved))
}

func (a *API) updateScenario(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	yamlBytes, err := readScenarioPayload(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := scenario.Parse(yamlBytes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid scenario: "+err.Error())
		return
	}
	if err := scenario.ValidateYAML(yamlBytes); err != nil {
		writeError(w, http.StatusBadRequest, "schema validation: "+err.Error())
		return
	}
	saved, err := a.Store.UpsertScenario(r.Context(), name, yamlBytes, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toScenarioResponse(saved))
}

// readScenarioPayload accepts either a raw YAML body (text/yaml,
// application/yaml, or no Content-Type) or a JSON wrapper {"yaml": "..."}.
// This dual shape is intentional: humans curl with raw YAML, clients send JSON.
func readScenarioPayload(body []byte) ([]byte, error) {
	if len(body) == 0 {
		return nil, errEmptyBody
	}
	// JSON wrapper?
	if body[0] == '{' {
		var req scenarioRequest
		if err := json.Unmarshal(body, &req); err == nil && req.YAML != "" {
			return []byte(req.YAML), nil
		}
	}
	return body, nil
}

var errEmptyBody = errStr("empty request body")

type errStr string

func (e errStr) Error() string { return string(e) }
