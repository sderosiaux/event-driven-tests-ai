package main

import (
	"fmt"
	"os"

	"github.com/event-driven-tests-ai/edt/pkg/controlplane"
	"github.com/spf13/cobra"
)

func newServeCmd() *cobra.Command {
	cfg := controlplane.Config{}
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
			if cfg.DBURL == "" {
				cfg.DBURL = os.Getenv("EDT_DB_URL")
			}
			s, err := controlplane.NewServerAuto(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			return s.Run(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&cfg.Addr, "addr", ":8080", "Listen address")
	cmd.Flags().StringVar(&cfg.DBURL, "db-url", "", "Postgres connection string (empty = in-memory dev mode)")
	cmd.Flags().BoolVar(&cfg.RequireAuth, "require-auth", false, "Require a valid bearer token on mutating endpoints")
	cmd.Flags().StringVar(&cfg.AdminToken, "admin-token", "", "Bootstrap admin token (also via EDT_ADMIN_TOKEN env)")
	return cmd
}
