package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "dev"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:          "edt",
		Short:        "event-driven-tests-ai — test Kafka, HTTP and AI-agent flows",
		SilenceUsage: true,
		Version:      version,
	}
	root.AddCommand(newValidateCmd())
	return root
}

func main() {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
