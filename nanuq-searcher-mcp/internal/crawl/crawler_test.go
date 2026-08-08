package crawl

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"nanuq-searcher-mcp/internal/domain"
)

// testPage is one path served by the crawl test server.
type testPage struct {
	body   string
	status int // 0 → 200 with body; else the status, no body
}

// newCrawlServer serves the given pages under one httptest server. robots is
// served at /robots.txt when non-empty (404 otherwise); paths absent from
// pages answer 404.
func newCrawlServer(t *testing.T, pages map[string]testPage, robots string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			if robots == "" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, robots)
			return
		}
		p, ok := pages[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if p.status != 0 {
			w.WriteHeader(p.status)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, p.body)
	}))
}

// testLogger returns a discard logger for the crawler.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testCrawlConfig returns a crawler config with the crawl defaults
// (8/100/3/true/true/15) and a test user agent.
func testCrawlConfig(workers int) CrawlConfig {
	return CrawlConfig{
		Workers:       workers,
		MaxPages:      100,
		MaxDepth:      3,
		SameHost:      true,
		RespectRobots: true,
		TimeoutSec:    15,
		UserAgent:     "nanuq-test/1.0",
	}
}

// pageByURL looks up a crawled page by its (normalized) URL.
func pageByURL(sm *domain.SiteMap, rawURL string) *domain.Page {
	u, err := NormalizeURL(rawURL)
	if err != nil {
		return nil
	}
	for i := range sm.Pages {
		if sm.Pages[i].URL == u {
			return &sm.Pages[i]
		}
	}
	return nil
}

// hasDuplicateURLs reports whether any page URL appears twice.
func hasDuplicateURLs(sm *domain.SiteMap) bool {
	seen := make(map[string]bool, len(sm.Pages))
	for _, p := range sm.Pages {
		if seen[p.URL] {
			return true
		}
		seen[p.URL] = true
	}
	return false
}

func TestCrawlVisitsInternalPages(t *testing.T) {
	ext := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<html><head><title>External</title></head><body></body></html>")
	}))
	defer ext.Close()
	extURL := ext.URL + "/x"

	pages := map[string]testPage{
		"/": {body: `<html><head><title>Root</title></head><body>
<h1>Root Heading</h1>
<a href="/a">a</a><a href="/b">b</a><a href="/c">c</a>
<a href="/missing">missing</a>
<a href="#top">fragment</a><a href="/">self</a>
<a href="mailto:x@y.z">mail</a><a href="javascript:void(0)">js</a><a href="">empty</a>
<a href="` + extURL + `">external</a>
</body></html>`},
		"/a":       {body: `<html><head><title>A</title></head><body><h2>Sub A</h2><a href="/a/child">child</a></body></html>`},
		"/b":       {body: `<html><head><title>B</title></head><body></body></html>`},
		"/c":       {body: `<html><head><title>C</title></head><body></body></html>`},
		"/a/child": {body: `<html><head><title>Child</title></head><body></body></html>`},
		// /missing intentionally absent → 404.
	}
	srv := newCrawlServer(t, pages, "")
	defer srv.Close()

	sm := Crawl(context.Background(), srv.URL+"/", testCrawlConfig(4), testLogger())
	if sm == nil {
		t.Fatal("Crawl returned nil")
	}
	if sm.Cancelled {
		t.Error("Cancelled = true, want false")
	}
	// root + a + b + c + missing(404, recorded) + a/child = 6; external and
	// the mailto/javascript/fragment/self links are filtered or deduped.
	if sm.Visited != 6 {
		t.Errorf("Visited = %d, want 6", sm.Visited)
	}
	if len(sm.Pages) != sm.Visited {
		t.Errorf("len(Pages) = %d, want %d", len(sm.Pages), sm.Visited)
	}
	if hasDuplicateURLs(sm) {
		t.Error("duplicate page URLs found")
	}

	root := pageByURL(sm, srv.URL+"/")
	if root == nil {
		t.Fatal("root page not visited")
	}
	if root.Depth != 0 {
		t.Errorf("root Depth = %d, want 0", root.Depth)
	}
	if root.Title != "Root" {
		t.Errorf("root Title = %q, want %q", root.Title, "Root")
	}
	if len(root.Headings) != 1 || root.Headings[0].Level != 1 || root.Headings[0].Text != "Root Heading" {
		t.Errorf("root Headings = %+v, want [{1 Root Heading}]", root.Headings)
	}

	if child := pageByURL(sm, srv.URL+"/a/child"); child == nil || child.Depth != 2 {
		t.Errorf("a/child = %+v, want visited at depth 2", child)
	}
	if a := pageByURL(sm, srv.URL+"/a"); a == nil || len(a.Headings) != 1 || a.Headings[0].Level != 2 {
		t.Errorf("/a headings not extracted: %+v", a)
	}

	// The broken link was attempted and its failure recorded, not fatal.
	broken := pageByURL(sm, srv.URL+"/missing")
	if broken == nil {
		t.Fatal("expected /missing to be recorded with an error")
	}
	if len(broken.Errors) == 0 || !strings.Contains(broken.Errors[0], "404") {
		t.Errorf("/missing Errors = %v, want mention of 404", broken.Errors)
	}

	// SameHost: the external page must not be visited.
	if p := pageByURL(sm, extURL); p != nil {
		t.Errorf("external page %s visited despite SameHost=true", p.URL)
	}
}

func TestCrawlRobotsDisallow(t *testing.T) {
	pages := map[string]testPage{
		"/":          {body: `<html><body><a href="/private/x">p</a><a href="/public/x">u</a></body></html>`},
		"/private/x": {body: `<html><body>private</body></html>`},
		"/public/x":  {body: `<html><body>public</body></html>`},
	}
	srv := newCrawlServer(t, pages, "User-agent: *\nDisallow: /private/\n")
	defer srv.Close()

	sm := Crawl(context.Background(), srv.URL+"/", testCrawlConfig(2), testLogger())

	for _, p := range sm.Pages {
		if strings.Contains(p.URL, "/private") {
			t.Errorf("disallowed page visited: %s", p.URL)
		}
	}
	if pageByURL(sm, srv.URL+"/public/x") == nil {
		t.Error("allowed page /public/x not visited")
	}
	if sm.Visited != 2 { // root + /public/x
		t.Errorf("Visited = %d, want 2", sm.Visited)
	}
}

func TestCrawlRespectsRobotsFalse(t *testing.T) {
	// With RespectRobots=false the disallowed path must still be visited.
	pages := map[string]testPage{
		"/":          {body: `<html><body><a href="/private/x">p</a></body></html>`},
		"/private/x": {body: `<html><body>private</body></html>`},
	}
	srv := newCrawlServer(t, pages, "User-agent: *\nDisallow: /\n")
	defer srv.Close()

	cfg := testCrawlConfig(1)
	cfg.RespectRobots = false
	sm := Crawl(context.Background(), srv.URL+"/", cfg, testLogger())

	if pageByURL(sm, srv.URL+"/private/x") == nil {
		t.Error("expected /private/x to be visited with RespectRobots=false")
	}
}

func TestCrawlMaxDepth(t *testing.T) {
	pages := map[string]testPage{
		"/":       {body: `<html><body><a href="/a">a</a></body></html>`},
		"/a":      {body: `<html><body><a href="/a/deep">deep</a></body></html>`},
		"/a/deep": {body: `<html><body>deep</body></html>`},
	}
	srv := newCrawlServer(t, pages, "")
	defer srv.Close()

	cfg := testCrawlConfig(1)
	cfg.MaxDepth = 1
	sm := Crawl(context.Background(), srv.URL+"/", cfg, testLogger())

	if sm.Visited != 2 {
		t.Errorf("Visited = %d, want 2 (root + /a)", sm.Visited)
	}
	if p := pageByURL(sm, srv.URL+"/a/deep"); p != nil {
		t.Errorf("page at depth 2 visited despite MaxDepth=1: %+v", p)
	}
}

func TestCrawlMaxPages(t *testing.T) {
	pages := map[string]testPage{"/": {body: `<html><body>`}}
	for i := 1; i <= 5; i++ {
		path := fmt.Sprintf("/p%d", i)
		pages[path] = testPage{body: "<html><body>p</body></html>"}
		pages["/"] = testPage{body: pages["/"].body + fmt.Sprintf(`<a href="%s">%s</a>`, path, path)}
	}
	srv := newCrawlServer(t, pages, "")
	defer srv.Close()

	cfg := testCrawlConfig(4)
	cfg.MaxPages = 3
	sm := Crawl(context.Background(), srv.URL+"/", cfg, testLogger())

	if sm.Visited != 3 {
		t.Errorf("Visited = %d, want 3 (cap)", sm.Visited)
	}
	if len(sm.Pages) != 3 {
		t.Errorf("len(Pages) = %d, want 3", len(sm.Pages))
	}
}

func TestCrawlSameHostDisabled(t *testing.T) {
	ext := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<html><head><title>External</title></head><body></body></html>")
	}))
	defer ext.Close()

	pages := map[string]testPage{
		"/": {body: `<html><body><a href="` + ext.URL + `/x">ext</a></body></html>`},
	}
	srv := newCrawlServer(t, pages, "")
	defer srv.Close()

	cfg := testCrawlConfig(2)
	cfg.SameHost = false
	sm := Crawl(context.Background(), srv.URL+"/", cfg, testLogger())

	if p := pageByURL(sm, ext.URL+"/x"); p == nil {
		t.Error("expected external page to be visited with SameHost=false")
	}
}

func TestCrawlHostErrors(t *testing.T) {
	pages := map[string]testPage{
		"/":      {body: `<html><body><a href="/ok">ok</a><a href="/boom">boom</a><a href="/after">after</a></body></html>`},
		"/ok":    {body: `<html><body>ok</body></html>`},
		"/boom":  {status: http.StatusInternalServerError},
		"/after": {body: `<html><body>after</body></html>`},
	}
	srv := newCrawlServer(t, pages, "")
	defer srv.Close()

	sm := Crawl(context.Background(), srv.URL+"/", testCrawlConfig(1), testLogger())

	if len(sm.HostErrors) != 1 {
		t.Fatalf("HostErrors = %v, want exactly one host", sm.HostErrors)
	}
	host := hostOf(srv)
	if msg, ok := sm.HostErrors[host]; !ok || !strings.Contains(msg, "500") {
		t.Errorf("HostErrors[%q] = %q, want message containing 500", host, msg)
	}
	if p := pageByURL(sm, srv.URL+"/after"); p != nil {
		t.Errorf("page /after visited after its host was stopped: %+v", p)
	}
	boom := pageByURL(sm, srv.URL+"/boom")
	if boom == nil || len(boom.Errors) == 0 {
		t.Errorf("expected /boom to be recorded with an error, got %+v", boom)
	}
	if sm.Visited != 3 { // root + /ok + /boom; /after dropped
		t.Errorf("Visited = %d, want 3", sm.Visited)
	}
}

func TestCrawlPacing(t *testing.T) {
	var mu sync.Mutex
	first := make(map[string]time.Time)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if _, seen := first[r.URL.Path]; !seen {
			first[r.URL.Path] = time.Now()
		}
		mu.Unlock()
		if r.URL.Path == "/robots.txt" {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, "User-agent: *\nCrawl-delay: 0.1\n")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<html><body><a href="/a">a</a><a href="/b">b</a></body></html>`)
	}))
	defer srv.Close()

	sm := Crawl(context.Background(), srv.URL+"/", testCrawlConfig(1), testLogger())
	if sm.Cancelled {
		t.Fatal("crawl cancelled unexpectedly")
	}

	mu.Lock()
	defer mu.Unlock()
	rootT, ok1 := first["/"]
	aT, ok2 := first["/a"]
	bT, ok3 := first["/b"]
	if !ok1 || !ok2 || !ok3 {
		t.Fatalf("missing page arrivals: root=%v /a=%v /b=%v", ok1, ok2, ok3)
	}
	// Crawl-delay is 100ms; allow slack for scheduler and the -race detector.
	const minGap = 80 * time.Millisecond
	if gap := aT.Sub(rootT); gap < minGap {
		t.Errorf("gap between / and /a = %v, want >= %v", gap, minGap)
	}
	if gap := bT.Sub(aT); gap < minGap {
		t.Errorf("gap between /a and /b = %v, want >= %v", gap, minGap)
	}
}

func TestCrawlCancellation(t *testing.T) {
	aRequested := make(chan struct{})
	releaseA := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/":
			_, _ = io.WriteString(w, `<html><body><a href="/a">a</a></body></html>`)
		case "/a":
			close(aRequested)
			<-releaseA // hold the request until the test cancels the context
			_, _ = io.WriteString(w, `<html><body>a</body></html>`)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan *domain.SiteMap, 1)
	go func() {
		done <- Crawl(ctx, srv.URL+"/", testCrawlConfig(1), testLogger())
	}()

	// /a in flight implies the root page was fetched and recorded: the crawl
	// is now provably mid-run.
	select {
	case <-aRequested:
	case <-time.After(5 * time.Second):
		t.Fatal("crawl never requested /a")
	}
	cancel()
	close(releaseA)

	select {
	case sm := <-done:
		if !sm.Cancelled {
			t.Error("Cancelled = false, want true")
		}
		if len(sm.Pages) == 0 {
			t.Error("expected a partial map with at least the root page")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Crawl did not return after cancellation")
	}
}

func TestCrawlCancelledBeforeStart(t *testing.T) {
	pages := map[string]testPage{
		"/":  {body: `<html><body><a href="/a">a</a></body></html>`},
		"/a": {body: `<html><body>a</body></html>`},
	}
	srv := newCrawlServer(t, pages, "")
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sm := Crawl(ctx, srv.URL+"/", testCrawlConfig(2), testLogger())

	if !sm.Cancelled {
		t.Error("Cancelled = false, want true for a pre-cancelled context")
	}
	if sm.Visited != 0 || len(sm.Pages) != 0 {
		t.Errorf("Visited = %d, Pages = %d, want 0/0", sm.Visited, len(sm.Pages))
	}
}

func TestCrawlConcurrent(t *testing.T) {
	// 12 interlinked same-host pages; p_i links to p_{i+1} and p_{i+2}.
	// MaxDepth=10 visits all of them (longest shortest-path depth is 6).
	pages := make(map[string]testPage, 12)
	for i := 0; i < 12; i++ {
		var sb strings.Builder
		sb.WriteString("<html><head><title>P</title></head><body>")
		for _, j := range []int{i + 1, i + 2} {
			if j < 12 {
				fmt.Fprintf(&sb, `<a href="/p%d">p%d</a>`, j, j)
			}
		}
		sb.WriteString("</body></html>")
		pages[fmt.Sprintf("/p%d", i)] = testPage{body: sb.String()}
	}
	srv := newCrawlServer(t, pages, "")
	defer srv.Close()

	cfg := testCrawlConfig(8)
	cfg.MaxDepth = 10
	cfg.MaxPages = 200
	sm := Crawl(context.Background(), srv.URL+"/p0", cfg, testLogger())

	if sm.Cancelled {
		t.Error("Cancelled = true, want false")
	}
	if sm.Visited != 12 {
		t.Errorf("Visited = %d, want 12", sm.Visited)
	}
	if hasDuplicateURLs(sm) {
		t.Error("duplicate page URLs under concurrency")
	}
	if len(sm.HostErrors) != 0 {
		t.Errorf("unexpected HostErrors: %v", sm.HostErrors)
	}
}

func TestCrawlInvalidRoot(t *testing.T) {
	sm := Crawl(context.Background(), "://bad", testCrawlConfig(1), testLogger())
	if sm == nil {
		t.Fatal("Crawl returned nil for an invalid root")
	}
	if sm.Visited != 0 || len(sm.Pages) != 0 {
		t.Errorf("Visited = %d, Pages = %d, want 0/0", sm.Visited, len(sm.Pages))
	}
}

// TestLogSafeURL verifies the DSG-012 log sanitization helper: query strings,
// fragments and userinfo are stripped so sensitive tokens never reach logs.
func TestLogSafeURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://example.com/page?token=secret&q=1#frag", "https://example.com/page"},
		{"http://user:pass@example.com:8080/a/b?x=1", "http://example.com:8080/a/b"},
		{"https://example.com/clean", "https://example.com/clean"},
		{"://bad", "<invalid>"},
	}
	for _, c := range cases {
		if got := logSafeURL(c.in); got != c.want {
			t.Errorf("logSafeURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
