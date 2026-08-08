package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"nanuq-engine/internal/config"
)

// autocompleteHandlerSpy is a test double for autocompleteSearcher that
// records the arguments it receives and returns a canned result. Tests run
// single-threaded, so no locking is needed.
type autocompleteHandlerSpy struct {
	backendName string
	query       string
	locale      string
	called      bool
	err         error
}

func (s *autocompleteHandlerSpy) search(backendName string, _ context.Context, query string, locale string) ([]string, error) {
	s.called = true
	s.backendName = backendName
	s.query = query
	s.locale = locale
	if s.err != nil {
		return nil, s.err
	}
	return []string{"sug1", "sug2"}, nil
}

func doAutocompleteRequest(t *testing.T, cfg *config.Config, spy *autocompleteHandlerSpy, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	autocompleteHandler(cfg, spy.search).ServeHTTP(rec, req)
	return rec
}

// TestAutocompleteXMLHttpRequest verifies REQ-019/CA-007: with the
// X-Requested-With: XMLHttpRequest header the handler responds with a flat
// JSON list of suggestions.
func TestAutocompleteXMLHttpRequest(t *testing.T) {
	cfg := &config.Config{}
	spy := &autocompleteHandlerSpy{}
	rec := doAutocompleteRequest(t, cfg, spy, "/autocompleter?q=nanuq", map[string]string{
		"X-Requested-With": "XMLHttpRequest",
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json; charset=utf-8")
	}
	var got []string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not a JSON list: %v (body=%s)", err, rec.Body.String())
	}
	want := []string{"sug1", "sug2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("body = %v, want %v", got, want)
	}
}

// TestAutocompleteOpenSearch verifies REQ-019/CA-007: without the AJAX header
// the handler responds with the OpenSearch suggestions JSON array
// [query, [sugs], [], [], {"google:suggestrelevance": [...]}].
func TestAutocompleteOpenSearch(t *testing.T) {
	cfg := &config.Config{}
	spy := &autocompleteHandlerSpy{}
	rec := doAutocompleteRequest(t, cfg, spy, "/autocompleter?q=nanuq", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-suggestions+json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/x-suggestions+json; charset=utf-8")
	}
	var payload []any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body is not JSON: %v (body=%s)", err, rec.Body.String())
	}
	if len(payload) != 5 {
		t.Fatalf("payload length = %d, want 5 (CA-007)", len(payload))
	}
	if payload[0] != "nanuq" {
		t.Errorf("payload[0] = %v, want %q", payload[0], "nanuq")
	}
	suggs, ok := payload[1].([]any)
	if !ok || len(suggs) != 2 || suggs[0] != "sug1" || suggs[1] != "sug2" {
		t.Errorf("payload[1] = %v, want [sug1 sug2]", payload[1])
	}
	meta, ok := payload[4].(map[string]any)
	if !ok {
		t.Fatalf("payload[4] = %v, want object", payload[4])
	}
	rel, ok := meta["google:suggestrelevance"].([]any)
	if !ok || len(rel) != 2 || rel[0].(float64) != 600 || rel[1].(float64) != 599 {
		t.Errorf("google:suggestrelevance = %v, want [600 599]", meta["google:suggestrelevance"])
	}
}

// TestAutocompleteEmptyQuery verifies a missing/empty q is answered with an
// empty list without consulting the backend (CA-007).
func TestAutocompleteEmptyQuery(t *testing.T) {
	cfg := &config.Config{}
	spy := &autocompleteHandlerSpy{}
	rec := doAutocompleteRequest(t, cfg, spy, "/autocompleter", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json; charset=utf-8")
	}
	if body := rec.Body.String(); body != "[]" {
		t.Errorf("body = %q, want %q", body, "[]")
	}
	if spy.called {
		t.Error("backend was called for an empty query")
	}
}

// TestAutocompleteDefaultBackend verifies an empty search.autocomplete config
// falls back to duckduckgo (TASK-013).
func TestAutocompleteDefaultBackend(t *testing.T) {
	cfg := &config.Config{} // Search.Autocomplete == ""
	spy := &autocompleteHandlerSpy{}
	doAutocompleteRequest(t, cfg, spy, "/autocompleter?q=nanuq", nil)
	if !spy.called {
		t.Fatal("backend was not called")
	}
	if spy.backendName != "duckduckgo" {
		t.Errorf("backend = %q, want %q", spy.backendName, "duckduckgo")
	}
}

// TestAutocompleteConfiguredBackend verifies cfg.Search.Autocomplete selects
// the backend.
func TestAutocompleteConfiguredBackend(t *testing.T) {
	cfg := &config.Config{Search: config.Search{Autocomplete: "bing"}}
	spy := &autocompleteHandlerSpy{}
	doAutocompleteRequest(t, cfg, spy, "/autocompleter?q=nanuq&locale=en", nil)
	if !spy.called {
		t.Fatal("backend was not called")
	}
	if spy.backendName != "bing" {
		t.Errorf("backend = %q, want %q", spy.backendName, "bing")
	}
	if spy.query != "nanuq" {
		t.Errorf("query = %q, want %q", spy.query, "nanuq")
	}
	if spy.locale != "en" {
		t.Errorf("locale = %q, want %q", spy.locale, "en")
	}
}

// TestAutocompleteBackendError verifies a backend failure degrades to an
// empty result set with a 200 (mirrors search_autocomplete swallowing
// backend errors in autocomplete.py L416-423). The response still follows the
// format selection: no AJAX header means OpenSearch with empty arrays.
func TestAutocompleteBackendError(t *testing.T) {
	cfg := &config.Config{}
	spy := &autocompleteHandlerSpy{err: errors.New("backend down")}
	rec := doAutocompleteRequest(t, cfg, spy, "/autocompleter?q=nanuq", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-suggestions+json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/x-suggestions+json; charset=utf-8")
	}
	var payload []any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body is not JSON: %v (body=%s)", err, rec.Body.String())
	}
	if len(payload) != 5 {
		t.Fatalf("payload length = %d, want 5 (CA-007)", len(payload))
	}
	if suggs, ok := payload[1].([]any); !ok || len(suggs) != 0 {
		t.Errorf("payload[1] = %v, want empty suggestions on backend error", payload[1])
	}
}

// TestAutocompleteRouteRegistered verifies the /autocompleter route is wired
// by RegisterAutocomplete (no backend call happens for an empty query, so no
// network is involved).
func TestAutocompleteRouteRegistered(t *testing.T) {
	cfg := &config.Config{}
	mux := http.NewServeMux()
	RegisterAutocomplete(mux, cfg)

	req := httptest.NewRequest(http.MethodGet, "/autocompleter", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); body != "[]" {
		t.Errorf("body = %q, want %q", body, "[]")
	}
}
