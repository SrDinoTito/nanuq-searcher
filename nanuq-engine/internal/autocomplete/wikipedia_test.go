package autocomplete

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// TestWikipediaBackend verifies the wikipedia MediaWiki opensearch port
// (autocomplete.py L355-379): query params and the [query, [sugs], [], [urls]]
// response shape.
func TestWikipediaBackend(t *testing.T) {
	var gotAction, gotFormat, gotSearch, gotLimit, gotNamespace string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAction = r.URL.Query().Get("action")
		gotFormat = r.URL.Query().Get("format")
		gotSearch = r.URL.Query().Get("search")
		gotLimit = r.URL.Query().Get("limit")
		gotNamespace = r.URL.Query().Get("namespace")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`["query",["sug1","sug2"],[],["url1","url2"]]`))
	}))
	defer srv.Close()

	results, err := wikipediaBackend(srv.URL)(context.Background(), "query", "")
	if err != nil {
		t.Fatalf("wikipediaBackend() error = %v", err)
	}
	want := []string{"sug1", "sug2"}
	if !reflect.DeepEqual(results, want) {
		t.Errorf("results = %v, want %v", results, want)
	}
	if gotAction != "opensearch" {
		t.Errorf("action param = %q, want %q", gotAction, "opensearch")
	}
	if gotFormat != "json" {
		t.Errorf("format param = %q, want %q", gotFormat, "json")
	}
	if gotSearch != "query" {
		t.Errorf("search param = %q, want %q", gotSearch, "query")
	}
	if gotLimit != "10" {
		t.Errorf("limit param = %q, want %q", gotLimit, "10")
	}
	if gotNamespace != "0" {
		t.Errorf("namespace param = %q, want %q", gotNamespace, "0")
	}
}

// TestWikipediaBackendShortResponse verifies a single-element response
// (len(data) <= 1 in the Python) yields an empty, non-nil result list.
func TestWikipediaBackendShortResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`["query"]`))
	}))
	defer srv.Close()

	results, err := wikipediaBackend(srv.URL)(context.Background(), "query", "")
	if err != nil {
		t.Fatalf("wikipediaBackend() error = %v", err)
	}
	if results == nil || len(results) != 0 {
		t.Errorf("results = %v, want empty non-nil slice", results)
	}
}
