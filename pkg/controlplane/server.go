// Package controlplane is the M2 component that historizes runs, exposes the
// REST API, hosts the web UI, and serves Prometheus metrics. The CLI talks to
// it via `--report-to`; workers register and pull assignments from it.
package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Config drives a Server. Zero values are sane for tests.
type Config struct {
	Addr   string        // listen address, defaults to ":8080"
	DBURL  string        // Postgres connection string; empty = run with no storage (dev only)
	Logger func(format string, args ...any)
}

// Server is the long-running control-plane process.
type Server struct {
	cfg    Config
	router chi.Router
	httpd  *http.Server
}

// NewServer wires the router and middleware. It does not bind the listener;
// call Run for that.
func NewServer(cfg Config) *Server {
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}
	if cfg.Logger == nil {
		cfg.Logger = func(string, ...any) {}
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	s := &Server{cfg: cfg, router: r}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.router.Get("/healthz", s.handleHealthz)
}

// Handler returns the underlying http.Handler for direct use in tests
// (httptest.NewServer(s.Handler())).
func (s *Server) Handler() http.Handler { return s.router }

// Run binds the listener and serves until ctx is cancelled. Returns the first
// non-graceful-shutdown error from http.Server.ListenAndServe.
func (s *Server) Run(ctx context.Context) error {
	s.httpd = &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           s.router,
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		s.cfg.Logger("control-plane listening on %s", s.cfg.Addr)
		errCh <- s.httpd.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.httpd.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("control-plane: serve: %w", err)
	}
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
