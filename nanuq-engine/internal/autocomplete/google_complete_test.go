package autocomplete

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// googleCompleteBody mirrors the JSON google's complete/search endpoint
// returns, including the window.google.ac.h(...) wrapper the real endpoint
// emits: the top-level array is [items, meta] where data[0] holds
// [suggestion, score, ...] tuples whose suggestion may carry HTML markup
// (autocomplete.py L129-157 strips it via lxml; the [..] slice drops the
// wrapper).
const googleCompleteBody = `window.google.ac.h([[["<b>ex</b>ample",0],["example test",0]],{"q":"example","k":1}])`

// TestGoogleCompleteBackend verifies the google_complete port: q/client/hl
// params, [..] body slicing and HTML stripping of suggestions.
func TestGoogleCompleteBackend(t *testing.T) {
	var gotQ, gotClient, gotHL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQ = r.URL.Query().Get("q")
		gotClient = r.URL.Query().Get("client")
		gotHL = r.URL.Query().Get("hl")
		_, _ = w.Write([]byte(googleCompleteBody))
	}))
	defer srv.Close()

	results, err := googleCompleteBackend(srv.URL)(context.Background(), "ex", "")
	if err != nil {
		t.Fatalf("googleCompleteBackend() error = %v", err)
	}
	want := []string{"example", "example test"}
	if !reflect.DeepEqual(results, want) {
		t.Errorf("results = %v, want %v", results, want)
	}
	if gotQ != "ex" {
		t.Errorf("q param = %q, want %q", gotQ, "ex")
	}
	if gotClient != "gws-wiz" {
		t.Errorf("client param = %q, want %q", gotClient, "gws-wiz")
	}
	if gotHL != "en" {
		t.Errorf("hl param = %q, want %q (default when locale empty)", gotHL, "en")
	}
}

// TestGoogleCompleteBackendLocale verifies hl is derived from the locale
// parameter (traits.get_language not ported, TASK-013).
func TestGoogleCompleteBackendLocale(t *testing.T) {
	var gotHL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHL = r.URL.Query().Get("hl")
		_, _ = w.Write([]byte(googleCompleteBody))
	}))
	defer srv.Close()

	if _, err := googleCompleteBackend(srv.URL)(context.Background(), "ex", "de"); err != nil {
		t.Fatalf("googleCompleteBackend() error = %v", err)
	}
	if gotHL != "de" {
		t.Errorf("hl param = %q, want %q", gotHL, "de")
	}
}
