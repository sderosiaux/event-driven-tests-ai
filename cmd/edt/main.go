package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

type hasExitCode interface{ ExitCode() int }

var version = "dev"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:          "edt",
		Short:        "event-driven-tests-ai — test Kafka, HTTP and AI-agent flows",
		SilenceUsage: true,
		Version:      version,
	}
	root.AddCommand(newValidateCmd())
	root.AddCommand(newRunCmd())
	root.AddCommand(newServeCmd())
	root.AddCommand(newWorkerCmd())
	return root
}

func main() {
	root := newRootCmd()
	root.SilenceErrors = true
	if err := root.Execute(); err != nil {
		var ex hasExitCode
		if errors.As(err, &ex) {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(ex.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
