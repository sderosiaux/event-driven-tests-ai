package report_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/event-driven-tests-ai/edt/pkg/checks"
	"github.com/event-driven-tests-ai/edt/pkg/report"
	"github.com/event-driven-tests-ai/edt/pkg/scenario"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mkReport(results []checks.CheckResult) *report.Report {
	return &report.Report{
		Scenario:  "demo",
		RunID:     "r-1",
		Mode:      "run",
		StartedAt: time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC),
		Checks:    results,
	}
}

func TestFinalizePassWhenAllOK(t *testing.T) {
	r := mkReport([]checks.CheckResult{{Name: "x", Passed: true, Severity: scenario.SeverityCritical}})
	r.Finalize()
	assert.Equal(t, report.StatusPass, r.Status)
	assert.Equal(t, 0, r.ExitCode)
}

func TestFinalizeFailOnCriticalFailure(t *testing.T) {
	r := mkReport([]checks.CheckResult{
		{Name: "w", Passed: false, Severity: scenario.SeverityWarning},
		{Name: "c", Passed: false, Severity: scenario.SeverityCritical},
	})
	r.Finalize()
	assert.Equal(t, report.StatusFail, r.Status)
	assert.Equal(t, 1, r.ExitCode)
}

func TestFinalizePassIgnoresWarningFailure(t *testing.T) {
	r := mkReport([]checks.CheckResult{
		{Name: "w", Passed: false, Severity: scenario.SeverityWarning},
	})
	r.Finalize()
	assert.Equal(t, report.StatusPass, r.Status)
	assert.Equal(t, 0, r.ExitCode)
}

func TestFinalizeErrorOnScenarioError(t *testing.T) {
	r := mkReport(nil)
	r.Error = "boom"
	r.Finalize()
	assert.Equal(t, report.StatusError, r.Status)
	assert.Equal(t, 2, r.ExitCode)
}

func TestWriteJSONRoundtrip(t *testing.T) {
	r := mkReport([]checks.CheckResult{{Name: "x", Passed: true, Severity: scenario.SeverityCritical}}).Finalize()
	var buf bytes.Buffer
	require.NoError(t, report.WriteJSON(&buf, r))

	var decoded report.Report
	require.NoError(t, json.Unmarshal(buf.Bytes(), &decoded))
	assert.Equal(t, r.Scenario, decoded.Scenario)
	assert.Equal(t, r.Status, decoded.Status)
	assert.Len(t, decoded.Checks, 1)
}

func TestWriteConsoleIncludesKeyFields(t *testing.T) {
	r := mkReport([]checks.CheckResult{
		{Name: "ack_p99", Passed: true, Severity: scenario.SeverityCritical},
		{Name: "orphans", Passed: false, Err: "boom", Severity: scenario.SeverityWarning},
	}).Finalize()
	var buf bytes.Buffer
	require.NoError(t, report.WriteConsole(&buf, r))

	s := buf.String()
	assert.Contains(t, s, "scenario: demo")
	assert.Contains(t, s, "[PASS] ack_p99")
	assert.Contains(t, s, "[FAIL] orphans")
	assert.Contains(t, s, "boom")
}
