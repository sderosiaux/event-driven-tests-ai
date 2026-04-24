// Package metrics owns the Prometheus collectors exposed by the control plane.
//
// Per design spec §12, workers do not expose /metrics directly — they push
// reports to the control plane, which is the sole scrape target. Cardinality
// is bounded by scenario+check labels under the user's control.
package metrics

import (
	"net/http"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/controlplane/storage"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/report"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry bundles the control-plane collectors. Use Register() to install
// them on the supplied prometheus.Registerer (defaults to a private registry
// so tests do not collide with the global one).
type Registry struct {
	reg *prometheus.Registry

	RunTotal           *prometheus.CounterVec
	RunDuration        *prometheus.HistogramVec
	CheckResultTotal   *prometheus.CounterVec
	CheckValue         *prometheus.GaugeVec
	WorkerUp           *prometheus.GaugeVec
	WorkerLastHB       *prometheus.GaugeVec
}

// New builds a Registry with all collectors registered on a fresh underlying
// prometheus.Registry.
func New() *Registry {
	r := &Registry{reg: prometheus.NewRegistry()}
	r.RunTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "edt_run_total",
		Help: "Number of edt runs by scenario, mode and result.",
	}, []string{"scenario", "mode", "result"})
	r.RunDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "edt_run_duration_seconds",
		Help:    "Run wall-clock duration.",
		Buckets: []float64{0.01, 0.1, 0.5, 1, 5, 30, 60, 300, 1800, 7200},
	}, []string{"scenario", "mode"})
	r.CheckResultTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "edt_check_result_total",
		Help: "Per-check pass/fail counter.",
	}, []string{"scenario", "check", "severity", "result"})
	r.CheckValue = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "edt_check_value",
		Help: "Last observed numeric value of a check (when applicable).",
	}, []string{"scenario", "check"})
	r.WorkerUp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "edt_worker_up",
		Help: "1 when the worker has heartbeated within its liveness window.",
	}, []string{"worker_id"})
	r.WorkerLastHB = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "edt_worker_last_heartbeat_seconds",
		Help: "Unix seconds of the last heartbeat received from the worker.",
	}, []string{"worker_id"})

	for _, c := range []prometheus.Collector{
		r.RunTotal, r.RunDuration, r.CheckResultTotal, r.CheckValue, r.WorkerUp, r.WorkerLastHB,
	} {
		r.reg.MustRegister(c)
	}
	return r
}

// Handler returns the http.Handler scraped by Prometheus.
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{})
}

// ObserveRun updates run-level counters/histograms from a finalized report.
func (r *Registry) ObserveRun(rep *report.Report) {
	if rep == nil {
		return
	}
	r.RunTotal.WithLabelValues(rep.Scenario, rep.Mode, string(rep.Status)).Inc()
	r.RunDuration.WithLabelValues(rep.Scenario, rep.Mode).Observe(rep.Duration.Seconds())
	for _, c := range rep.Checks {
		result := "pass"
		if !c.Passed {
			result = "fail"
		}
		r.CheckResultTotal.WithLabelValues(rep.Scenario, c.Name, string(c.Severity), result).Inc()
		if v, ok := numeric(c.Value); ok {
			r.CheckValue.WithLabelValues(rep.Scenario, c.Name).Set(v)
		}
	}
}

// ObserveWorkers refreshes the worker liveness gauges from the storage view.
// liveness is the threshold beyond which a worker is considered down.
func (r *Registry) ObserveWorkers(workers []storage.Worker, liveness float64) {
	r.WorkerUp.Reset()
	r.WorkerLastHB.Reset()
	for _, w := range workers {
		r.WorkerLastHB.WithLabelValues(w.ID).Set(float64(w.LastHeartbeat.Unix()))
		up := 0.0
		if !w.LastHeartbeat.IsZero() && (timeNow().Sub(w.LastHeartbeat).Seconds() <= liveness) {
			up = 1.0
		}
		r.WorkerUp.WithLabelValues(w.ID).Set(up)
	}
}

// numeric coerces best-effort numeric check values for gauge exposure.
// Booleans and non-numeric strings are silently skipped.
func numeric(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	}
	return 0, false
}
