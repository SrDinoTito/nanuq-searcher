package autocomplete

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// TestDuckDuckGoBackend verifies the duckduckgo port (autocomplete.py L109-126)
// against a local httptest server: query params and the [prefix, [sugs]]
// response shape.
func TestDuckDuckGoBackend(t *testing.T) {
	var gotQ, gotKL, gotType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQ = r.URL.Query().Get("q")
		gotKL = r.URL.Query().Get("kl")
		gotType = r.URL.Query().Get("type")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`["prefix",["sug1","sug 2","sug3"]]`))
	}))
	defer srv.Close()

	results, err := duckduckgoBackend(srv.URL)(context.Background(), "nanuq", "")
	if err != nil {
		t.Fatalf("duckduckgoBackend() error = %v", err)
	}
	want := []string{"sug1", "sug 2", "sug3"}
	if !reflect.DeepEqual(results, want) {
		t.Errorf("results = %v, want %v", results, want)
	}
	if gotQ != "nanuq" {
		t.Errorf("q param = %q, want %q", gotQ, "nanuq")
	}
	if gotKL != "wt-wt" {
		t.Errorf("kl param = %q, want %q (traits not ported, always All regions)", gotKL, "wt-wt")
	}
	if gotType != "list" {
		t.Errorf("type param = %q, want %q", gotType, "list")
	}
}

// TestDuckDuckGoBackendShortResponse verifies a response with only the prefix
// element ([len(j) <= 1] in the Python) yields an empty, non-nil result list.
func TestDuckDuckGoBackendShortResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`["prefix"]`))
	}))
	defer srv.Close()

	results, err := duckduckgoBackend(srv.URL)(context.Background(), "nanuq", "")
	if err != nil {
		t.Fatalf("duckduckgoBackend() error = %v", err)
	}
	if results == nil || len(results) != 0 {
		t.Errorf("results = %v, want empty non-nil slice", results)
	}
}
