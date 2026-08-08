// Package metrics exposes Prometheus instrumentation for nanuq-engine
// (REQ-024, TASK-020).
//
// The package follows the Prometheus client_golang conventions: a single
// Metrics struct holds the collectors, New registers them on
// prometheus.DefaultRegisterer with the "nanuq_" prefix, and Handler serves
// the /metrics endpoint via promhttp. Labels are snake_case (engine, reason)
// per Prometheus naming conventions. No reflection (REQ-NF-004).
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics bundles the Prometheus collectors used to instrument search
// activity per engine (REQ-024).
type Metrics struct {
	engineSuccess   *prometheus.CounterVec
	engineError     *prometheus.CounterVec
	engineTimeout   *prometheus.CounterVec
	engineSuspended *prometheus.CounterVec
	searchDuration  *prometheus.HistogramVec
	searchTotal     prometheus.Counter
}

// New creates a Metrics, registers its collectors on
// prometheus.DefaultRegisterer and returns it. It must be called once per
// process; a second call would panic with a duplicate-registration error,
// matching client_golang behaviour.
func New() *Metrics {
	m := &Metrics{
		engineSuccess: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "nanuq",
			Subsystem: "engine",
			Name:      "success_total",
			Help:      "Total number of successful engine responses.",
		}, []string{"engine"}),
		engineError: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "nanuq",
			Subsystem: "engine",
			Name:      "error_total",
			Help:      "Total number of engine errors.",
		}, []string{"engine", "reason"}),
		engineTimeout: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "nanuq",
			Subsystem: "engine",
			Name:      "timeout_total",
			Help:      "Total number of engine timeouts.",
		}, []string{"engine"}),
		engineSuspended: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "nanuq",
			Subsystem: "engine",
			Name:      "suspended_total",
			Help:      "Total number of times an engine was suspended.",
		}, []string{"engine", "reason"}),
		searchDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "nanuq",
			Subsystem: "engine",
			Name:      "search_duration_seconds",
			Help:      "Duration of searches in seconds, per engine.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"engine"}),
		searchTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "nanuq",
			Name:      "search_total",
			Help:      "Total number of searches executed.",
		}),
	}

	prometheus.MustRegister(
		m.engineSuccess,
		m.engineError,
		m.engineTimeout,
		m.engineSuspended,
		m.searchDuration,
		m.searchTotal,
	)

	return m
}

// RecordEngineSuccess counts a successful response from engine.
func (m *Metrics) RecordEngineSuccess(engine string) {
	m.engineSuccess.WithLabelValues(engine).Inc()
}

// RecordEngineError counts an error from engine, tagged with a snake_case
// reason (e.g. "http_error", "parse_error").
func (m *Metrics) RecordEngineError(engine, reason string) {
	m.engineError.WithLabelValues(engine, reason).Inc()
}

// RecordEngineTimeout counts a watchdog timeout for engine.
func (m *Metrics) RecordEngineTimeout(engine string) {
	m.engineTimeout.WithLabelValues(engine).Inc()
}

// RecordEngineSuspended counts a suspension of engine, tagged with the
// reason (e.g. "timeout", "429", "exception", "init").
func (m *Metrics) RecordEngineSuspended(engine, reason string) {
	m.engineSuspended.WithLabelValues(engine, reason).Inc()
}

// ObserveSearchDuration records the duration of a search for engine in
// seconds.
func (m *Metrics) ObserveSearchDuration(engine string, seconds float64) {
	m.searchDuration.WithLabelValues(engine).Observe(seconds)
}

// RecordSearch counts a completed search request.
func (m *Metrics) RecordSearch() {
	m.searchTotal.Inc()
}

// Handler returns an http.Handler that serves the registered Prometheus
// metrics in the text exposition format (promhttp.Handler).
func Handler() http.Handler {
	return promhttp.Handler()
}
