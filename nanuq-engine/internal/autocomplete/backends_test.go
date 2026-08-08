package autocomplete

import (
	"context"
	"errors"
	"testing"
)

// TestBackendsRegistryHasFive verifies the registry exposes exactly the five
// backends mandated by REQ-019 (TASK-013).
func TestBackendsRegistryHasFive(t *testing.T) {
	want := []string{"duckduckgo", "google_complete", "wikipedia", "bing", "brave"}
	if len(backends) != len(want) {
		t.Fatalf("len(backends) = %d, want %d", len(backends), len(want))
	}
	for _, name := range want {
		if _, ok := backends[name]; !ok {
			t.Errorf("backends missing key %q", name)
		}
	}
}

// TestSearchUnknownBackend verifies Search returns a wrapped
// ErrUnknownBackend for unregistered backend names.
func TestSearchUnknownBackend(t *testing.T) {
	got, err := Search("does-not-exist", context.Background(), "nanuq", "")
	if err == nil {
		t.Fatal("Search() with unknown backend: want error, got nil")
	}
	if !errors.Is(err, ErrUnknownBackend) {
		t.Errorf("Search() error = %v, want errors.Is(err, ErrUnknownBackend)", err)
	}
	if got != nil {
		t.Errorf("Search() results = %v, want nil", got)
	}
}
