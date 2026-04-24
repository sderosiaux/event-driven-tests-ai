package main

import (
	"fmt"
	"os"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
	"github.com/spf13/cobra"
)

func newValidateCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate a scenario YAML file against the JSON Schema",
		RunE: func(cmd *cobra.Command, args []string) error {
			if file == "" {
				return fmt.Errorf("--file is required")
			}
			raw, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			// Env expansion so templated scenarios can validate against host
			// env vars. Same rules as `edt run`: only ${ALL_CAPS} placeholders.
			b := []byte(expandHostEnv(string(raw)))
			// Parse (catches syntax, unknown fields).
			if _, err := scenario.Parse(b); err != nil {
				return err
			}
			// Schema-validate (catches semantics, missing required).
			if err := scenario.ValidateYAML(b); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "scenario %s: ok\n", file)
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "Path to scenario YAML")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}
