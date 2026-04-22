package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/event-driven-tests-ai/edt/pkg/checks"
	"github.com/event-driven-tests-ai/edt/pkg/controlplane/storage"
	"github.com/event-driven-tests-ai/edt/pkg/report"
	"github.com/event-driven-tests-ai/edt/pkg/scenario"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func scrape(t *testing.T, r *Registry) string {
	t.Helper()
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func TestObserveRunUpdatesCounters(t *testing.T) {
	r := New()
	rep := &report.Report{
		Scenario: "demo", Mode: "run", Status: report.StatusPass,
		Duration: 1500 * time.Millisecond,
		Checks: []checks.CheckResult{
			{Name: "p99", Severity: scenario.SeverityCritical, Passed: true, Value: 42.0},
			{Name: "warn", Severity: scenario.SeverityWarning, Passed: false},
		},
	}
	r.ObserveRun(rep)
	body := scrape(t, r)

	assert.Contains(t, body, `edt_run_total{mode="run",result="pass",scenario="demo"} 1`)
	assert.Contains(t, body, `edt_check_result_total{check="p99",result="pass",scenario="demo",severity="critical"} 1`)
	assert.Contains(t, body, `edt_check_result_total{check="warn",result="fail",scenario="demo",severity="warning"} 1`)
	assert.Contains(t, body, `edt_check_value{check="p99",scenario="demo"} 42`)
}

func TestObserveWorkersLiveness(t *testing.T) {
	r := New()
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	timeNow = func() time.Time { return now }
	t.Cleanup(func() { timeNow = func() time.Time { return time.Now() } })

	r.ObserveWorkers([]storage.Worker{
		{ID: "w-fresh", LastHeartbeat: now.Add(-5 * time.Second)},
		{ID: "w-stale", LastHeartbeat: now.Add(-2 * time.Minute)},
	}, 30 /* seconds */)

	body := scrape(t, r)
	assert.Contains(t, body, `edt_worker_up{worker_id="w-fresh"} 1`)
	assert.Contains(t, body, `edt_worker_up{worker_id="w-stale"} 0`)
}

func TestObserveRunNilDoesNotPanic(t *testing.T) {
	r := New()
	assert.NotPanics(t, func() { r.ObserveRun(nil) })
	// Empty registry yields empty body — that's fine; we only assert no panic.
}

func TestNumericCoercion(t *testing.T) {
	cases := []any{1, int64(2), int32(3), float32(4.5), float64(5.5)}
	for _, c := range cases {
		v, ok := numeric(c)
		assert.True(t, ok, "%T", c)
		assert.Greater(t, v, 0.0)
	}
	if _, ok := numeric("nope"); ok {
		t.Fatal("strings should not coerce")
	}
	if _, ok := numeric(true); ok {
		t.Fatal("bool should not coerce")
	}
}

func TestHandlerEmitsHelpLines(t *testing.T) {
	r := New()
	r.ObserveRun(&report.Report{Scenario: "x", Mode: "run", Status: report.StatusPass})
	body := scrape(t, r)
	assert.True(t, strings.Contains(body, "# HELP edt_run_total"))
	assert.True(t, strings.Contains(body, "# TYPE edt_run_total counter"))
}
