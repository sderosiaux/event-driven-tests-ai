package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// newBatchRunCmd is a convenience: iterate a directory of scenarios and run
// each one via the same pipeline as `edt run`. Useful for seeding a control
// plane with realistic data in demo/dev environments without spawning
// multiple containers or driving a shell loop.
func newBatchRunCmd() *cobra.Command {
	var (
		dir        string
		reportTo   string
		reportTok  string
		brokers    string
		continueOn bool
	)
	cmd := &cobra.Command{
		Use:   "batch-run",
		Short: "Run every scenario YAML in a directory. Handy for seeding demo runs.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if dir == "" {
				return fmt.Errorf("--dir is required")
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				return fmt.Errorf("batch-run: read %s: %w", dir, err)
			}
			var paths []string
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
					continue
				}
				paths = append(paths, filepath.Join(dir, e.Name()))
			}
			sort.Strings(paths) // numeric prefixes on filenames drive order.

			var failures []string
			for _, p := range paths {
				fmt.Fprintf(cmd.ErrOrStderr(), "--- batch-run: %s\n", p)
				f := &runFlags{
					file:             p,
					reportTo:         reportTo,
					reportToken:      reportTok,
					bootstrapServers: brokers,
					format:           "text",
				}
				if err := doRun(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), f); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "!!! %s failed: %v\n", p, err)
					failures = append(failures, filepath.Base(p))
					if !continueOn {
						return err
					}
				}
			}
			if len(failures) > 0 {
				return fmt.Errorf("batch-run: %d scenario(s) failed: %s", len(failures), strings.Join(failures, ", "))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "Directory containing scenario YAML files")
	cmd.Flags().StringVar(&reportTo, "report-to", "", "Control plane base URL to push each run's report")
	cmd.Flags().StringVar(&reportTok, "report-token", "", "Bearer token for --report-to")
	cmd.Flags().StringVar(&brokers, "bootstrap-servers", "", "Override spec.connectors.kafka.bootstrap_servers for every scenario")
	cmd.Flags().BoolVar(&continueOn, "continue-on-error", true, "Continue running remaining scenarios after a failure (default true)")
	return cmd
}
