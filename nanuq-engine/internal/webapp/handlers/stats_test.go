// Package handlers_test exercises the /stats and /stats/errors routes of
// TASK-012c (REQ-017) through the real Server.Handler() route table.
//
// The package is external (handlers_test) so it can import package webapp
// without creating the webapp -> handlers import cycle. Helpers
// (newTestServer, doGet) are shared with misc_test.go.
package handlers_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestStats checks that GET /stats renders the statistics page (200). No
// metrics are accumulated yet (TASK-020), so only the page placeholder is
// asserted.
func TestStats(t *testing.T) {
	rec := doGet(t, newTestServer(t), "/stats")
	if rec.Code != http.StatusOK {
		t.Fatalf("stats status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<h2>Statistics</h2>") {
		t.Errorf("stats body missing heading: %s", body)
	}
}

// TestStatsErrors checks that GET /stats/errors renders the engine-errors
// page (200) with an empty list (TASK-020 will populate it).
func TestStatsErrors(t *testing.T) {
	rec := doGet(t, newTestServer(t), "/stats/errors")
	if rec.Code != http.StatusOK {
		t.Fatalf("stats/errors status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<h2>Engine errors</h2>") {
		t.Errorf("stats/errors body missing heading: %s", body)
	}
	if !strings.Contains(body, "No engine errors recorded.") {
		t.Errorf("stats/errors body missing empty-state message: %s", body)
	}
}

// TestStatsUnknownPathIs404 checks that a path under /stats that is not a
// registered route falls through to the catch-all and returns 404.
func TestStatsUnknownPathIs404(t *testing.T) {
	rec := doGet(t, newTestServer(t), "/stats/foo")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("/stats/foo status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
