// Package handlers_test exercises the /preferences route of TASK-012c
// (REQ-017) through the real Server.Handler() route table.
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

// TestPreferences checks that GET /preferences renders the preferences page
// (200) with the configured instance name (REQ-017).
func TestPreferences(t *testing.T) {
	rec := doGet(t, newTestServer(t), "/preferences")
	if rec.Code != http.StatusOK {
		t.Fatalf("preferences status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<h2>Preferences</h2>") {
		t.Errorf("preferences body missing heading: %s", body)
	}
	if !strings.Contains(body, testInstance) {
		t.Errorf("preferences body missing instance name %q: %s", testInstance, body)
	}
}

// TestPreferencesCategories checks that the available categories are listed
// on the preferences page.
func TestPreferencesCategories(t *testing.T) {
	rec := doGet(t, newTestServer(t), "/preferences")
	if rec.Code != http.StatusOK {
		t.Fatalf("preferences status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	for _, category := range []string{"general", "images", "videos"} {
		if !strings.Contains(body, "<li>"+category+"</li>") {
			t.Errorf("preferences body missing category %q: %s", category, body)
		}
	}
}
