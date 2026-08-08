// Package handlers_test exercises the /engine_descriptions.json route of
// TASK-012c (REQ-017) through the real Server.Handler() route table.
//
// The package is external (handlers_test) so it can import package webapp
// without creating the webapp -> handlers import cycle. Helpers
// (newTestServer, doGet) are shared with misc_test.go.
package handlers_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestEngineDescriptions checks that GET /engine_descriptions.json returns
// 200 with a valid JSON body. No engines are loaded this phase (TASK-011/
// 022), so the payload must be an empty list serialised as [].
func TestEngineDescriptions(t *testing.T) {
	rec := doGet(t, newTestServer(t), "/engine_descriptions.json")
	if rec.Code != http.StatusOK {
		t.Fatalf("engine_descriptions status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("engine_descriptions Content-Type = %q", ct)
	}
	var payload []any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("engine_descriptions body is not valid JSON: %v", err)
	}
	if len(payload) != 0 {
		t.Errorf("engine_descriptions payload = %v, want empty list", payload)
	}
}
