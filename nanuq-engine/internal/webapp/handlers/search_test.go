// Package handlers_test exercises the /search route (TASK-012b) through
// the real Server.Handler() route table, like misc_test.go.
//
// The search service is built with no engine processors (empty engine
// config) and a nil requester: every search yields an empty, valid
// ResultContainer (internal/search/search_test.go pattern).
package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"nanuq-engine/internal/bang"
	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
	"nanuq-engine/internal/search"
	"nanuq-engine/internal/webapp"
)

// allFormats is the format whitelist used by the json/csv/rss tests.
// The real settings.yml only advertises [html]; passing the full set here
// keeps those tests from tripping the EC-008 403 guard.
var allFormats = []string{"html", "json", "csv", "rss"}

// testBangResolver implements search.BangResolver with a single known bang
// (ddg -> DuckDuckGo), mirroring testBangStore in search_test.go.
type testBangResolver struct{}

func (testBangResolver) Lookup(name string) (bang.BangDef, bool) {
	if name == "ddg" {
		return bang.BangDef{URL: "https://duckduckgo.com/?q=\x02", Rank: 1}, true
	}
	return bang.BangDef{}, false
}

func (testBangResolver) GetBangURL(name, query string) (string, bool) {
	if name != "ddg" {
		return "", false
	}
	return "https://duckduckgo.com/?q=" + url.QueryEscape(query), true
}

// emptyCatalog implements search.EngineCatalog with no engines. It must be
// non-nil: SearchService.buildSearchQuery dereferences the catalog.
type emptyCatalog struct{}

func (emptyCatalog) Has(string) bool                           { return false }
func (emptyCatalog) ResolveShortcut(string) (string, bool)     { return "", false }
func (emptyCatalog) EnginesInCategory(string) ([]string, bool) { return nil, false }

// newSearchTestServer builds a Server wired with an engine-less search
// service, the fake bang resolver and the empty catalog.
func newSearchTestServer(t *testing.T, formats []string) *webapp.Server {
	t.Helper()
	cfg := &config.Config{
		General: config.General{InstanceName: testInstance},
		Search:  config.Search{Formats: formats},
	}
	store := testBangResolver{}
	catalog := emptyCatalog{}
	svc := search.New(engine.New(), store, catalog, cfg, nil, nil)
	return webapp.New(cfg, svc, store, catalog)
}

func TestSearchJSONBasic(t *testing.T) {
	srv := newSearchTestServer(t, allFormats)
	rec := doGet(t, srv, "/search?q=test&format=json")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	// CA-003: all seven keys are always present.
	for _, key := range []string{"query", "results", "answers", "corrections", "infoboxes", "suggestions", "unresponsive_engines"} {
		if _, ok := payload[key]; !ok {
			t.Errorf("missing key %q in JSON response", key)
		}
	}
	if got := payload["query"]; got != "test" {
		t.Errorf("query = %v, want test", got)
	}
}

func TestSearchJSONNoResults(t *testing.T) {
	srv := newSearchTestServer(t, allFormats)
	rec := doGet(t, srv, "/search?q=nomatch&format=json")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	// Empty result lists must serialise as [] (never null) so the client
	// can rely on a stable shape.
	for _, key := range []string{"results", "answers", "corrections", "infoboxes", "suggestions", "unresponsive_engines"} {
		arr, ok := payload[key].([]any)
		if !ok || len(arr) != 0 {
			t.Errorf("%s = %v, want empty array", key, payload[key])
		}
	}
	if got := payload["query"]; got != "nomatch" {
		t.Errorf("query = %v, want nomatch", got)
	}
}

func TestSearchFormatNotAllowed(t *testing.T) {
	srv := newSearchTestServer(t, allFormats)
	rec := doGet(t, srv, "/search?q=test&format=xml")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d (EC-008)", rec.Code, http.StatusForbidden)
	}
}

func TestSearchExternalBangRedirect(t *testing.T) {
	srv := newSearchTestServer(t, nil)
	rec := doGet(t, srv, "/search?q=!!ddg+hola")
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusFound, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "duckduckgo.com") {
		t.Errorf("Location = %q, want redirect to duckduckgo.com", loc)
	}
}

func TestSearchHTML(t *testing.T) {
	srv := newSearchTestServer(t, nil)
	rec := doGet(t, srv, "/search?q=hello+world")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "hello world") {
		t.Errorf("html body missing query: %s", body)
	}
	if !strings.Contains(body, "No results found") {
		t.Errorf("html body missing no-results marker: %s", body)
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	srv := newSearchTestServer(t, nil)
	rec := doGet(t, srv, "/search?q=")
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusFound, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want /", loc)
	}
}

func TestSearchCSV(t *testing.T) {
	srv := newSearchTestServer(t, allFormats)
	rec := doGet(t, srv, "/search?q=test&format=csv")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/csv" {
		t.Errorf("Content-Type = %q, want application/csv", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "searx_-_test.csv") {
		t.Errorf("Content-Disposition = %q, want attachment;Filename=searx_-_test.csv", cd)
	}
	if body := rec.Body.String(); !strings.HasPrefix(body, "title,url,content") {
		t.Errorf("csv missing header row: %s", body)
	}
}

func TestSearchRSS(t *testing.T) {
	srv := newSearchTestServer(t, allFormats)
	rec := doGet(t, srv, "/search?q=test&format=rss")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.HasPrefix(body, "<?xml") {
		t.Errorf("rss must start with XML declaration: %s", body)
	}
	if !strings.Contains(body, `<rss version="2.0">`) {
		t.Errorf("rss missing channel: %s", body)
	}
}
