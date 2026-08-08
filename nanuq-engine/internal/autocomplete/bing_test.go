package autocomplete

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// TestBingBackend verifies the bing AS/Suggestions port (autocomplete.py
// L61-78): qry/csr/cvid params and PUA character stripping on the 'q' field.
func TestBingBackend(t *testing.T) {
	var gotQry, gotCSR, gotCVID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQry = r.URL.Query().Get("qry")
		gotCSR = r.URL.Query().Get("csr")
		gotCVID = r.URL.Query().Get("cvid")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"s":[{"q":"sug\ue0001\ue001"},{"q":"sug2"}]}`))
	}))
	defer srv.Close()

	results, err := bingBackend(srv.URL)(context.Background(), "sug", "")
	if err != nil {
		t.Fatalf("bingBackend() error = %v", err)
	}
	want := []string{"sug1", "sug2"}
	if !reflect.DeepEqual(results, want) {
		t.Errorf("results = %v, want %v", results, want)
	}
	if gotQry != "sug" {
		t.Errorf("qry param = %q, want %q", gotQry, "sug")
	}
	if gotCSR != "1" {
		t.Errorf("csr param = %q, want %q", gotCSR, "1")
	}
	if len(gotCVID) != 32 {
		t.Errorf("cvid param length = %d, want 32", len(gotCVID))
	}
	for _, c := range gotCVID {
		if !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", c) {
			t.Errorf("cvid contains invalid char %q", c)
		}
	}
}

// TestBingBackendNoSuggestions verifies a response without the 's' key yields
// an empty, non-nil result list.
func TestBingBackendNoSuggestions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"q":"sug"}`))
	}))
	defer srv.Close()

	results, err := bingBackend(srv.URL)(context.Background(), "sug", "")
	if err != nil {
		t.Fatalf("bingBackend() error = %v", err)
	}
	if results == nil || len(results) != 0 {
		t.Errorf("results = %v, want empty non-nil slice", results)
	}
}
