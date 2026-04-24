package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/controlplane/storage"
	"github.com/go-chi/chi/v5"
)

// MountTokens registers the admin-only token routes. Caller decides whether to
// gate them with RequireRole(storage.RoleAdmin) at mount time.
func (a *API) MountTokens(r chi.Router) {
	r.Route("/api/v1/tokens", func(r chi.Router) {
		r.Get("/", a.listTokens)
		r.Post("/", a.issueToken)
		r.Delete("/{id}", a.revokeToken)
	})
}

type issueRequest struct {
	Role storage.Role `json:"role"`
	Note string       `json:"note,omitempty"`
}

func (a *API) issueToken(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<16))
	var req issueRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !validRole(req.Role) {
		writeError(w, http.StatusBadRequest, "invalid role; allowed: admin|editor|viewer|worker")
		return
	}
	t, err := a.Store.IssueToken(r.Context(), req.Role, req.Note)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":         t.ID,
		"role":       t.Role,
		"note":       t.Note,
		"created_at": t.CreatedAt,
		"plaintext":  t.Plaintext, // returned ONLY here; never again
	})
}

func (a *API) listTokens(w http.ResponseWriter, r *http.Request) {
	ts, err := a.Store.ListTokens(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, len(ts))
	for i, t := range ts {
		out[i] = map[string]any{
			"id":         t.ID,
			"role":       t.Role,
			"note":       t.Note,
			"created_at": t.CreatedAt,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) revokeToken(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := a.Store.RevokeToken(r.Context(), id); err != nil {
		writeError(w, statusForStorageErr(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validRole(r storage.Role) bool {
	switch r {
	case storage.RoleAdmin, storage.RoleEditor, storage.RoleViewer, storage.RoleWorker:
		return true
	}
	return false
}
