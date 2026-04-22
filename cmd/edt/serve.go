package main

import (
	"fmt"

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
			s := controlplane.NewServer(cfg)
			return s.Run(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&cfg.Addr, "addr", ":8080", "Listen address")
	cmd.Flags().StringVar(&cfg.DBURL, "db-url", "", "Postgres connection string (empty = in-memory dev mode)")
	return cmd
}
