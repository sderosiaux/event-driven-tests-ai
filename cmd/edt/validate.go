package main

import (
	"fmt"
	"os"

	"github.com/event-driven-tests-ai/edt/pkg/scenario"
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
			b, err := os.ReadFile(file)
			if err != nil {
				return err
			}
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
