package autocomplete

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// TestBraveBackend verifies the brave api/suggest port (autocomplete.py
// L81-94): the country=all cookie and the [[...], [sugs]] response shape.
func TestBraveBackend(t *testing.T) {
	var gotQ, gotCountry string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQ = r.URL.Query().Get("q")
		if c, err := r.Cookie("country"); err == nil {
			gotCountry = c.Value
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[["p"],["sug1","sug2"]]`))
	}))
	defer srv.Close()

	results, err := braveBackend(srv.URL)(context.Background(), "sug", "")
	if err != nil {
		t.Fatalf("braveBackend() error = %v", err)
	}
	want := []string{"sug1", "sug2"}
	if !reflect.DeepEqual(results, want) {
		t.Errorf("results = %v, want %v", results, want)
	}
	if gotQ != "sug" {
		t.Errorf("q param = %q, want %q", gotQ, "sug")
	}
	if gotCountry != "all" {
		t.Errorf("country cookie = %q, want %q", gotCountry, "all")
	}
}
