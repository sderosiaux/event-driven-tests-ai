package report

import (
	"fmt"
	"io"
	"strings"
)

// WriteConsole emits a terse human-readable summary.
// Intentionally colour-free: CI logs and redirect-to-file friendly.
func WriteConsole(w io.Writer, r *Report) error {
	var b strings.Builder
	fmt.Fprintf(&b, "scenario: %s\n", r.Scenario)
	if r.Mode != "" {
		fmt.Fprintf(&b, "mode:     %s\n", r.Mode)
	}
	fmt.Fprintf(&b, "run_id:   %s\n", r.RunID)
	fmt.Fprintf(&b, "status:   %s (exit %d)\n", r.Status, r.ExitCode)
	fmt.Fprintf(&b, "duration: %s\n", r.Duration)
	fmt.Fprintf(&b, "events:   %d\n", r.EventCount)
	if r.Error != "" {
		fmt.Fprintf(&b, "error:    %s\n", r.Error)
	}
	if len(r.Checks) > 0 {
		fmt.Fprintln(&b, "checks:")
		for _, c := range r.Checks {
			mark := "PASS"
			if c.Failed() {
				mark = "FAIL"
			}
			fmt.Fprintf(&b, "  [%s] %-30s %s", mark, c.Name, c.Severity)
			if c.Err != "" {
				fmt.Fprintf(&b, " — %s", c.Err)
			}
			fmt.Fprintln(&b)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}
