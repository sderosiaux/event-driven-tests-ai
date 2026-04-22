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

	"github.com/event-driven-tests-ai/edt/pkg/controlplane/api"
	"github.com/event-driven-tests-ai/edt/pkg/controlplane/metrics"
	"github.com/event-driven-tests-ai/edt/pkg/controlplane/storage"
	"github.com/event-driven-tests-ai/edt/pkg/controlplane/ui"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	_ "github.com/event-driven-tests-ai/edt/pkg/report" // referenced by api package; ensures consistent dep graph
)

// Config drives a Server. Zero values are sane for tests.
type Config struct {
	Addr        string // listen address, defaults to ":8080"
	DBURL       string // Postgres connection string; empty = run with no storage (dev only)
	RequireAuth bool   // enforce bearer-token role middleware on mutating endpoints
	AdminToken  string // bootstrap admin bearer token (also via EDT_ADMIN_TOKEN)
	Logger      func(format string, args ...any)
}

// Server is the long-running control-plane process.
type Server struct {
	cfg     Config
	router  chi.Router
	httpd   *http.Server
	store   storage.Storage
	api     *api.API
	metrics *metrics.Registry
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

	store := storage.NewMemStore() // M2 default; Postgres backend lands in M2-T2b.
	mreg := metrics.New()
	s := &Server{cfg: cfg, router: r, store: store, api: api.NewWithMetrics(store, mreg), metrics: mreg}
	s.bootstrapAdmin()
	s.routes()
	return s
}

// NewServerAuto picks a backend from cfg: Postgres when DBURL is set,
// in-memory otherwise. Returns the constructed Server or an error if the
// Postgres backend cannot be opened.
func NewServerAuto(ctx context.Context, cfg Config) (*Server, error) {
	if cfg.DBURL == "" {
		return NewServer(cfg), nil
	}
	pg, err := storage.NewPGStore(ctx, cfg.DBURL)
	if err != nil {
		return nil, err
	}
	return NewServerWithStorage(cfg, pg), nil
}

// NewServerWithStorage lets callers (tests, future Postgres entrypoint) inject
// a Storage rather than the default in-memory store.
func NewServerWithStorage(cfg Config, store storage.Storage) *Server {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))
	if cfg.Logger == nil {
		cfg.Logger = func(string, ...any) {}
	}
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}
	mreg := metrics.New()
	s := &Server{cfg: cfg, router: r, store: store, api: api.NewWithMetrics(store, mreg), metrics: mreg}
	s.bootstrapAdmin()
	s.routes()
	return s
}

// bootstrapAdmin seeds the configured admin token on startup so the server
// can require auth from the very first request.
func (s *Server) bootstrapAdmin() {
	if s.cfg.AdminToken == "" {
		return
	}
	t, err := s.store.IssueTokenWithPlaintext(context.Background(), s.cfg.AdminToken, storage.RoleAdmin, "bootstrap")
	if err != nil {
		s.cfg.Logger("bootstrap admin token: %v", err)
		return
	}
	s.cfg.Logger("bootstrap admin token id=%s installed", t.ID)
}

func (s *Server) routes() {
	s.router.Get("/healthz", s.handleHealthz)
	s.router.Method(http.MethodGet, "/metrics", s.metrics.Handler())

	// Optional auth: mutating endpoints require editor; tokens management
	// requires admin. When RequireAuth is false the API is fully open — handy
	// for local dev and the in-process tests.
	editor := s.maybeRequire(storage.RoleEditor)
	admin := s.maybeRequire(storage.RoleAdmin)
	worker := s.maybeRequire(storage.RoleWorker)

	s.router.Group(func(r chi.Router) {
		r.Use(editor)
		s.api.MountScenarios(r)
	})
	s.router.Group(func(r chi.Router) {
		r.Use(worker)
		s.api.MountRuns(r)
		s.api.MountWorkers(r)
	})
	s.router.Group(func(r chi.Router) {
		r.Use(admin)
		s.api.MountTokens(r)
	})

	// UI is always public (read-only).
	staticHandler := http.StripPrefix("/ui/static/", http.FileServer(ui.StaticFS()))
	s.router.Handle("/ui/static/*", staticHandler)
	s.router.Get("/", s.serveIndex)
	s.router.Get("/ui/runs", s.serveIndex)
	s.router.Get("/ui/workers", s.serveIndex)
}

// maybeRequire returns a no-op middleware when auth is disabled, otherwise
// returns the role-checking middleware.
func (s *Server) maybeRequire(min storage.Role) func(http.Handler) http.Handler {
	if !s.cfg.RequireAuth {
		return func(next http.Handler) http.Handler { return next }
	}
	return s.api.RequireRole(min)
}

func (s *Server) serveIndex(w http.ResponseWriter, _ *http.Request) {
	body, err := ui.Index()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(body)
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
