package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "dev"

func newRootCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "edt",
		Short:        "event-driven-tests-ai — test Kafka, HTTP and AI-agent flows",
		SilenceUsage: true,
		Version:      version,
	}
}

func main() {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
