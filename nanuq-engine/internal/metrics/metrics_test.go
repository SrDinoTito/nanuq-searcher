package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// testMetrics is lazily initialized so New() — which registers on
// prometheus.DefaultRegisterer — runs exactly once per test binary. A second
// New() would panic with a duplicate-registration error, so tests share the
// single instance.
var testMetrics *Metrics

func getMetrics(t *testing.T) *Metrics {
	t.Helper()
	if testMetrics == nil {
		testMetrics = New()
	}
	return testMetrics
}

// seedSeries forces one label series per vector collector so the gathered
// families are non-empty. An empty CounterVec/HistogramVec produces no
// output at all, which would make registration checks fail.
func seedSeries(m *Metrics) {
	m.engineSuccess.WithLabelValues("_seed")
	m.engineError.WithLabelValues("_seed", "_seed")
	m.engineTimeout.WithLabelValues("_seed")
	m.engineSuspended.WithLabelValues("_seed", "_seed")
	m.searchDuration.WithLabelValues("_seed")
}

// TestNewDoesNotPanic verifies that constructing and registering the
// collectors does not panic (registration on DefaultRegisterer included).
func TestNewDoesNotPanic(t *testing.T) {
	m := getMetrics(t)
	if m == nil {
		t.Fatal("New() returned nil")
	}
	seedSeries(m)
	// Sanity: every collector is registered and gatherable.
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("Gather() failed: %v", err)
	}
	names := make(map[string]bool)
	for _, f := range families {
		names[f.GetName()] = true
	}
	for _, want := range []string{
		"nanuq_engine_success_total",
		"nanuq_engine_error_total",
		"nanuq_engine_timeout_total",
		"nanuq_engine_suspended_total",
		"nanuq_engine_search_duration_seconds",
		"nanuq_search_total",
	} {
		if !names[want] {
			t.Errorf("metric %q not registered", want)
		}
	}
}

// TestHandler serves the /metrics endpoint with the nanuq_ metrics exposed.
func TestHandler(t *testing.T) {
	m := getMetrics(t)
	seedSeries(m)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Handler() status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain...", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "nanuq_engine_success_total") {
		t.Error("handler body missing nanuq_engine_success_total")
	}
	if !strings.Contains(body, "nanuq_search_total") {
		t.Error("handler body missing nanuq_search_total")
	}
}

// TestRecordEngineSuccess verifies the counter increments per engine label.
func TestRecordEngineSuccess(t *testing.T) {
	m := getMetrics(t)

	before := testutil.ToFloat64(m.engineSuccess.WithLabelValues("ddg"))
	m.RecordEngineSuccess("ddg")
	after := testutil.ToFloat64(m.engineSuccess.WithLabelValues("ddg"))
	if after != before+1 {
		t.Errorf("success counter = %v, want %v", after, before+1)
	}

	// Different engine label is an independent series.
	if got := testutil.ToFloat64(m.engineSuccess.WithLabelValues("google")); got != 0 {
		t.Errorf("google success counter = %v, want 0 (isolated per-engine series)", got)
	}
}

// TestRecordEngineError verifies the counter increments and carries the
// snake_case engine and reason labels.
func TestRecordEngineError(t *testing.T) {
	m := getMetrics(t)

	before := testutil.ToFloat64(m.engineError.WithLabelValues("ddg", "http_error"))
	m.RecordEngineError("ddg", "http_error")
	after := testutil.ToFloat64(m.engineError.WithLabelValues("ddg", "http_error"))
	if after != before+1 {
		t.Errorf("error counter = %v, want %v", after, before+1)
	}

	// Label cardinality check via the gatherer: the exposed series must carry
	// engine=ddg and reason=http_error.
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("Gather() failed: %v", err)
	}
	for _, f := range families {
		if f.GetName() != "nanuq_engine_error_total" {
			continue
		}
		for _, metric := range f.GetMetric() {
			labels := make(map[string]string)
			for _, lp := range metric.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}
			if labels["engine"] == "ddg" && labels["reason"] == "http_error" {
				return // found the expected series with correct labels
			}
		}
	}
	t.Error("gathered nanuq_engine_error_total missing series {engine=ddg, reason=http_error}")
}

// TestRecordEngineTimeout verifies the timeout counter increments.
func TestRecordEngineTimeout(t *testing.T) {
	m := getMetrics(t)

	before := testutil.ToFloat64(m.engineTimeout.WithLabelValues("ddg"))
	m.RecordEngineTimeout("ddg")
	after := testutil.ToFloat64(m.engineTimeout.WithLabelValues("ddg"))
	if after != before+1 {
		t.Errorf("timeout counter = %v, want %v", after, before+1)
	}
}

// TestRecordEngineSuspended verifies the suspension counter increments with
// reason label.
func TestRecordEngineSuspended(t *testing.T) {
	m := getMetrics(t)

	before := testutil.ToFloat64(m.engineSuspended.WithLabelValues("ddg", "429"))
	m.RecordEngineSuspended("ddg", "429")
	after := testutil.ToFloat64(m.engineSuspended.WithLabelValues("ddg", "429"))
	if after != before+1 {
		t.Errorf("suspended counter = %v, want %v", after, before+1)
	}
}

// TestObserveSearchDuration verifies the histogram observes the duration.
// The histogram is verified through the gatherer because a
// HistogramVec.WithLabelValues returns a prometheus.Observer, which
// testutil.ToFloat64 cannot read directly.
func TestObserveSearchDuration(t *testing.T) {
	m := getMetrics(t)

	m.ObserveSearchDuration("ddg", 0.42)

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("Gather() failed: %v", err)
	}
	for _, f := range families {
		if f.GetName() != "nanuq_engine_search_duration_seconds" {
			continue
		}
		for _, metric := range f.GetMetric() {
			labels := make(map[string]string)
			for _, lp := range metric.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}
			if labels["engine"] != "ddg" {
				continue
			}
			if got := metric.GetHistogram().GetSampleCount(); got != 1 {
				t.Errorf("sample_count = %d, want 1", got)
			}
			return
		}
	}
	t.Error("gathered histogram missing series {engine=ddg}")
}

// TestRecordSearch verifies the total search counter increments.
func TestRecordSearch(t *testing.T) {
	m := getMetrics(t)

	before := testutil.ToFloat64(m.searchTotal)
	m.RecordSearch()
	after := testutil.ToFloat64(m.searchTotal)
	if after != before+1 {
		t.Errorf("search total = %v, want %v", after, before+1)
	}
}
