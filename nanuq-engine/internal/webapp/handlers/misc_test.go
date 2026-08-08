// Package handlers_test exercises the webapp misc routes through the real
// Server.Handler() route table (TASK-012a, REQ-017).
//
// The package is an external test package (handlers_test) so it can import
// nanuq-engine/internal/webapp without creating the webapp -> handlers
// import cycle.
package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/webapp"
)

const testInstance = "nanuq-test"

// newTestServer builds a Server backed by a minimal config. The search
// service, bang store and catalog are nil: no misc route touches them.
func newTestServer(t *testing.T) *webapp.Server {
	t.Helper()
	cfg := &config.Config{
		General: config.General{InstanceName: testInstance},
	}
	return webapp.New(cfg, nil, nil, nil)
}

func doGet(t *testing.T, srv *webapp.Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestHealthz(t *testing.T) {
	rec := doGet(t, newTestServer(t), "/healthz")
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "OK" {
		t.Errorf("healthz body = %q, want %q", got, "OK")
	}
}

func TestIndex(t *testing.T) {
	rec := doGet(t, newTestServer(t), "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("index status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<form") {
		t.Errorf("index body missing <form: %s", body)
	}
	if !strings.Contains(body, `action="/search"`) {
		t.Errorf("index form must target /search: %s", body)
	}
	if !strings.Contains(body, testInstance) {
		t.Errorf("index body missing instance name %q: %s", testInstance, body)
	}
}

func TestIndexUnknownPathIs404(t *testing.T) {
	rec := doGet(t, newTestServer(t), "/no-such-page")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown path status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestRobots(t *testing.T) {
	rec := doGet(t, newTestServer(t), "/robots.txt")
	if rec.Code != http.StatusOK {
		t.Fatalf("robots status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "User-agent: *\nAllow: /\n" {
		t.Errorf("robots body = %q", got)
	}
}

func TestOpenSearch(t *testing.T) {
	rec := doGet(t, newTestServer(t), "/opensearch.xml")
	if rec.Code != http.StatusOK {
		t.Fatalf("opensearch status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.HasPrefix(body, "<?xml") {
		t.Errorf("opensearch must start with XML declaration: %s", body)
	}
	if !strings.Contains(body, "OpenSearchDescription") {
		t.Errorf("opensearch missing root element: %s", body)
	}
	if !strings.Contains(body, testInstance) {
		t.Errorf("opensearch missing instance name: %s", body)
	}
	if !strings.Contains(body, "{searchTerms}") {
		t.Errorf("opensearch missing {searchTerms} template: %s", body)
	}
}

func TestConfig(t *testing.T) {
	rec := doGet(t, newTestServer(t), "/config")
	if rec.Code != http.StatusOK {
		t.Fatalf("config status = %d, want %d", rec.Code, http.StatusOK)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("config body is not valid JSON: %v", err)
	}
	got, ok := payload["instance_name"].(string)
	if !ok || got != testInstance {
		t.Errorf("config instance_name = %v, want %q", payload["instance_name"], testInstance)
	}
}

func TestManifest(t *testing.T) {
	rec := doGet(t, newTestServer(t), "/manifest.json")
	if rec.Code != http.StatusOK {
		t.Fatalf("manifest status = %d, want %d", rec.Code, http.StatusOK)
	}
	var m map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("manifest body is not valid JSON: %v", err)
	}
	if m["name"] != testInstance {
		t.Errorf("manifest name = %q, want %q", m["name"], testInstance)
	}
}

func TestFaviconNoContent(t *testing.T) {
	rec := doGet(t, newTestServer(t), "/favicon.ico")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("favicon status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestClientCSSWildcard(t *testing.T) {
	rec := doGet(t, newTestServer(t), "/client/abc123.css")
	if rec.Code != http.StatusOK {
		t.Fatalf("client css status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "" {
		t.Errorf("client css body = %q, want empty", got)
	}
}

func TestLogoWildcard404(t *testing.T) {
	rec := doGet(t, newTestServer(t), "/logo/200x200")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("logo status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestRSSXSLStub(t *testing.T) {
	rec := doGet(t, newTestServer(t), "/rss.xsl")
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("rss.xsl status = %d, want %d", rec.Code, http.StatusNotImplemented)
	}
}

func TestClearCookies(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/clear_cookies", nil)
	req.AddCookie(&http.Cookie{Name: "preferences", Value: "a=1"})
	req.AddCookie(&http.Cookie{Name: "session", Value: "x"})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("clear_cookies status = %d, want %d (redirect)", rec.Code, http.StatusFound)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("clear_cookies Location = %q, want /", loc)
	}
	var cleared []string
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge == -1 {
			cleared = append(cleared, c.Name)
		}
	}
	if len(cleared) != 2 {
		t.Errorf("expected 2 expired cookies, got %v", cleared)
	}
}
