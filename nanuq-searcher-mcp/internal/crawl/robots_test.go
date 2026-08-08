package crawl

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient spins up an httptest server serving the given robots.txt body
// (or 404 when body is empty) and a RobotsClient configured with the given
// user-agent. It returns the client, the server, a request counter and the
// captured log buffer.
//
// NOTE: the RobotsClient always tries https://<host>/robots.txt first, which
// fails at the transport level against a plain httptest server, then falls
// back to http — so each cache miss produces exactly one httptest request.
func newTestClient(t *testing.T, ua, body string) (*RobotsClient, *httptest.Server, *int32, *bytes.Buffer) {
	t.Helper()
	var hits int32
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.URL.Path != "/robots.txt" {
			http.NotFound(w, r)
			return
		}
		if body == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	rc := NewRobotsClient(RobotsConfig{UserAgent: ua}, logger)
	return rc, srv, &hits, &logBuf
}

// hostOf derives the cache key (lowercase host:port) from an httptest URL.
func hostOf(srv *httptest.Server) string {
	return strings.TrimPrefix(srv.URL, "http://")
}

func TestAllowedDisallowGroup(t *testing.T) {
	rc, srv, _, _ := newTestClient(t, "nanuq-mcp/0.1.0", "User-agent: *\nDisallow: /private/\n")
	base := srv.URL

	allowed, err := rc.Allowed(context.Background(), base+"/private/x")
	if err != nil {
		t.Fatalf("Allowed(/private/x): %v", err)
	}
	if allowed {
		t.Error("expected /private/x to be DISALLOWED by group '*'")
	}

	allowed, err = rc.Allowed(context.Background(), base+"/public/x")
	if err != nil {
		t.Fatalf("Allowed(/public/x): %v", err)
	}
	if !allowed {
		t.Error("expected /public/x to be allowed (no matching rule)")
	}
}

func TestAllowedUASpecificGroup(t *testing.T) {
	// The MCP UA must match the "nanuq-mcp" group (longest prefix), NOT the
	// catch-all "*" group that disallows everything — otherwise /public/x
	// would be blocked.
	const robots = "User-agent: nanuq-mcp\nDisallow: /private/\n\nUser-agent: *\nDisallow: /\n"
	rc, srv, _, _ := newTestClient(t, "nanuq-mcp/0.1.0", robots)
	base := srv.URL

	allowed, err := rc.Allowed(context.Background(), base+"/private/x")
	if err != nil {
		t.Fatalf("Allowed(/private/x): %v", err)
	}
	if allowed {
		t.Error("expected /private/x to be DISALLOWED by the 'nanuq-mcp' group")
	}

	allowed, err = rc.Allowed(context.Background(), base+"/public/x")
	if err != nil {
		t.Fatalf("Allowed(/public/x): %v", err)
	}
	if !allowed {
		t.Error("expected /public/x to be allowed: the 'nanuq-mcp' group has no rule, '*' group must not apply")
	}
}

func TestAllowedNoRobotsTxt404(t *testing.T) {
	rc, srv, _, logBuf := newTestClient(t, "nanuq-mcp/0.1.0", "")
	base := srv.URL

	allowed, err := rc.Allowed(context.Background(), base+"/anything")
	if err != nil {
		t.Fatalf("Allowed: %v", err)
	}
	if !allowed {
		t.Error("expected allowed=true on 404 (fail-open)")
	}
	if s := logBuf.String(); !strings.Contains(s, "no robots.txt") {
		t.Errorf("expected Info log about missing robots.txt, got:\n%s", s)
	}
}

func TestAllowedUnreachableFailOpen(t *testing.T) {
	// A listener that is immediately closed: every connection attempt fails,
	// both for https and the http fallback → fail-open with a Warning.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close() // server is now down

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	rc := NewRobotsClient(RobotsConfig{UserAgent: "nanuq-mcp/0.1.0"}, logger)

	allowed, err := rc.Allowed(context.Background(), "http://"+addr+"/x")
	if err != nil {
		t.Fatalf("Allowed: %v", err)
	}
	if !allowed {
		t.Error("expected allowed=true when robots.txt is unreachable (fail-open)")
	}
	if s := logBuf.String(); !strings.Contains(s, "unreachable") {
		t.Errorf("expected Warn log containing 'unreachable', got:\n%s", s)
	}
}

func TestAllowedMalformedFailOpen(t *testing.T) {
	// "Disallow:" before any "User-agent:" line → parser reports
	// "Disallow before User-agent at token ..." → FromBytes returns a
	// ParseError → fail-open with a Warning.
	rc, srv, _, logBuf := newTestClient(t, "nanuq-mcp/0.1.0", "Disallow: /private\n")
	base := srv.URL

	allowed, err := rc.Allowed(context.Background(), base+"/private/x")
	if err != nil {
		t.Fatalf("Allowed: %v", err)
	}
	if !allowed {
		t.Error("expected allowed=true on malformed robots.txt (fail-open)")
	}
	if s := logBuf.String(); !strings.Contains(s, "malformed") {
		t.Errorf("expected Warn log containing 'malformed', got:\n%s", s)
	}
}

func TestCrawlDelay(t *testing.T) {
	rc, srv, _, _ := newTestClient(t, "nanuq-mcp/0.1.0", "User-agent: *\nCrawl-delay: 2\n")
	host := hostOf(srv)

	// CrawlDelay is consult-only: warm the cache via Allowed first.
	allowed, err := rc.Allowed(context.Background(), srv.URL+"/x")
	if err != nil {
		t.Fatalf("Allowed: %v", err)
	}
	if !allowed {
		t.Fatal("expected allowed")
	}

	if d := rc.CrawlDelay(host); d != 2*time.Second {
		t.Errorf("CrawlDelay = %v, want 2s", d)
	}
}

func TestCrawlDelayZeroWhenAbsent(t *testing.T) {
	rc, srv, _, _ := newTestClient(t, "nanuq-mcp/0.1.0", "User-agent: *\nDisallow: /private/\n")
	host := hostOf(srv)

	if _, err := rc.Allowed(context.Background(), srv.URL+"/x"); err != nil {
		t.Fatalf("Allowed: %v", err)
	}
	if d := rc.CrawlDelay(host); d != 0 {
		t.Errorf("CrawlDelay = %v, want 0 (no Crawl-delay declared)", d)
	}
}

func TestCachePerHostNoRedownload(t *testing.T) {
	rc, srv, hits, _ := newTestClient(t, "nanuq-mcp/0.1.0", "User-agent: *\nDisallow: /private/\n")
	base := srv.URL

	for i := 0; i < 2; i++ {
		if _, err := rc.Allowed(context.Background(), base+"/public/x"); err != nil {
			t.Fatalf("Allowed #%d: %v", i, err)
		}
	}
	if n := atomic.LoadInt32(hits); n != 1 {
		t.Errorf("robots.txt fetched %d times, want 1 (cache must avoid re-download)", n)
	}
}

func TestAllowedInvalidURL(t *testing.T) {
	rc, _, _, _ := newTestClient(t, "nanuq-mcp/0.1.0", "User-agent: *\n")
	if _, err := rc.Allowed(context.Background(), "://not-a-url"); err == nil {
		t.Error("expected error for invalid URL")
	}
}

func TestAllowedConcurrent(t *testing.T) {
	rc, srv, _, _ := newTestClient(t, "nanuq-mcp/0.1.0", "User-agent: *\nDisallow: /private/\n")
	base := srv.URL

	const workers = 20
	// Preallocated per-index slots: each goroutine writes only its own slot,
	// so position i always belongs to worker i.
	results := make([]bool, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := "/public/x"
			if i%2 == 0 {
				path = "/private/x"
			}
			results[i], errs[i] = rc.Allowed(context.Background(), base+path)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("Allowed #%d: %v", i, err)
		}
	}
	for i, allowed := range results {
		want := i%2 != 0 // odd workers queried /public/x → allowed
		if allowed != want {
			t.Errorf("result #%d = %v, want %v", i, allowed, want)
		}
	}
}

func TestCloseClearsCache(t *testing.T) {
	rc, srv, hits, _ := newTestClient(t, "nanuq-mcp/0.1.0", "User-agent: *\n")
	base := srv.URL

	if _, err := rc.Allowed(context.Background(), base+"/x"); err != nil {
		t.Fatalf("Allowed: %v", err)
	}
	rc.Close()
	if _, err := rc.Allowed(context.Background(), base+"/x"); err != nil {
		t.Fatalf("Allowed after Close: %v", err)
	}
	if n := atomic.LoadInt32(hits); n != 2 {
		t.Errorf("robots.txt fetched %d times after Close, want 2 (cache cleared)", n)
	}
}

// TestAllowsLargeRobotsTxt ensures bodies larger than maxRobotsTxtSize are
// truncated, not buffered unbounded (NFR-002 bounded memory).
func TestAllowsLargeRobotsTxt(t *testing.T) {
	var buf strings.Builder
	buf.WriteString("User-agent: *\n")
	for i := 0; i < maxRobotsTxtSize/16+1; i++ {
		fmt.Fprintf(&buf, "# padding line %d\n", i)
	}
	rc, srv, _, _ := newTestClient(t, "nanuq-mcp/0.1.0", buf.String())
	base := srv.URL

	allowed, err := rc.Allowed(context.Background(), base+"/x")
	if err != nil {
		t.Fatalf("Allowed: %v", err)
	}
	if !allowed {
		t.Error("expected allowed on oversized robots.txt (body truncated, prefix still parseable)")
	}
}
