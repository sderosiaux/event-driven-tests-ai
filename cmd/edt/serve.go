package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/controlplane"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/controlplane/storage"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
	"github.com/spf13/cobra"
)

func newServeCmd() *cobra.Command {
	cfg := controlplane.Config{}
	var seedDir string
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
			return s.Run(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&cfg.Addr, "addr", ":8080", "Listen address")
	cmd.Flags().StringVar(&cfg.DBURL, "db-url", "", "Postgres connection string (empty = in-memory dev mode)")
	cmd.Flags().BoolVar(&cfg.RequireAuth, "require-auth", false, "Require a valid bearer token on mutating endpoints")
	cmd.Flags().StringVar(&cfg.AdminToken, "admin-token", "", "Bootstrap admin token (also via EDT_ADMIN_TOKEN env)")
	cmd.Flags().StringVar(&cfg.WorkerToken, "worker-token", "", "Bootstrap worker-scoped token (also via EDT_WORKER_TOKEN env)")
	cmd.Flags().StringVar(&seedDir, "seed-dir", "", "Directory of scenario YAML files to upsert on startup (also via EDT_SEED_DIR env)")
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
