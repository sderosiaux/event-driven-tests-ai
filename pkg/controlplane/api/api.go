// Package api owns the control-plane REST handlers. Handlers depend on a
// Storage abstraction so unit tests can use the in-memory implementation.
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/event-driven-tests-ai/edt/pkg/controlplane/storage"
)

// API holds the dependencies shared across handlers.
type API struct {
	Store storage.Storage
}

// New builds an API bound to the given Storage.
func New(s storage.Storage) *API { return &API{Store: s} }

// writeJSON serialises v as JSON. It is the canonical response helper used
// across all handlers so all responses carry a consistent Content-Type.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError emits a structured `{error: "..."}` body with the given status.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// statusForStorageErr maps storage sentinel errors to an HTTP status.
func statusForStorageErr(err error) int {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}
