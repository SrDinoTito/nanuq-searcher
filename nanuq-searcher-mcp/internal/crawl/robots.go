package crawl

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/temoto/robotstxt"
)

// maxRobotsTxtSize bounds the memory spent on a single robots.txt body
// (NFR-002: bounded memory). 1 MiB is far beyond any realistic robots.txt
// while keeping the fetch cheap. The LimitReader reads at most
// maxRobotsTxtSize+1 bytes so oversize files are detected by length.
const maxRobotsTxtSize = 1 << 20 // 1 MiB

// defaultRobotsTimeout is the per-request timeout applied when
// RobotsConfig.HTTPTimeout is zero.
const defaultRobotsTimeout = 10 * time.Second

// RobotsConfig configures a RobotsClient.
type RobotsConfig struct {
	// UserAgent is the MCP's own user-agent (NFR-003), identical to the one
	// the fetch tool uses, so that hosts that tailor robots.txt per agent
	// return the correct group.
	UserAgent string
	// HTTPTimeout is the per-request timeout; zero defaults to 10s (EC-006
	// allows up to 15s; robots.txt fetches are small, so 10s is ample).
	HTTPTimeout time.Duration
}

// robotsEntry is the cached outcome for a single host.
//
// data == nil means the host carries no effective restrictions: the fetch
// failed and we failed open (REQ-013), the server answered 4xx (no
// robots.txt), or the body was unparseable. err records the reason when the
// entry was produced by a failure; it is nil for a healthy empty entry.
type robotsEntry struct {
	data *robotstxt.RobotsData
	err  error
	ts   time.Time
}

// RobotsClient applies the robots.txt rules (REQ-013) for one MCP instance.
//
// The per-host cache is a plain in-memory map guarded by a mutex. It is
// TRANSITORY: it lives for the lifetime of the RobotsClient (typically one
// crawl call / one tool invocation). It deliberately has no TTL and no
// persistence — persistent cross-request caching is out of scope by
// D-07/CST-004. Close releases the cache.
//
// The client is safe for concurrent use. A first concurrent burst for the
// same host may each fetch robots.txt (no per-host singleflight) — the
// entries are identical, the duplicate fetch is benign and the map writes are
// mutex-guarded.
type RobotsClient struct {
	ua      string
	client  *http.Client
	log     *slog.Logger
	mu      sync.Mutex
	entries map[string]*robotsEntry
}

// NewRobotsClient builds a RobotsClient. A nil log is replaced with a
// discard logger so the client can always log safely.
func NewRobotsClient(cfg RobotsConfig, log *slog.Logger) *RobotsClient {
	timeout := cfg.HTTPTimeout
	if timeout <= 0 {
		timeout = defaultRobotsTimeout
	}
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &RobotsClient{
		ua:      cfg.UserAgent,
		client:  &http.Client{Timeout: timeout},
		log:     log,
		entries: make(map[string]*robotsEntry),
	}
}

// Allowed reports whether fetching rawURL is permitted under the host's
// robots.txt (REQ-013). It returns true when the host has no robots.txt,
// when the file cannot be reached or parsed (fail-open, DECISION-SPEC-003),
// or when no rule matches the URL path.
//
// The check applies the group selected for the client's UserAgent, with
// fallback to "*" and, if no group matches, no restrictions (RFC 9309).
// Matching uses only the URL path (query strings are not part of robots
// rules).
//
// Callers that intentionally disable robots respect (respect_robots=false,
// decided at the tool layer, TASK-013) skip this check per call — the client
// itself always applies robots rules.
func (rc *RobotsClient) Allowed(ctx context.Context, rawURL string) (bool, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false, fmt.Errorf("robots: invalid URL %q: %w", rawURL, err)
	}
	if u.Host == "" {
		return false, fmt.Errorf("robots: URL %q has no host", rawURL)
	}
	// Cache key: lowercase host (hostnames are case-insensitive; the port is
	// part of u.Host, so different ports of one host are distinct entries).
	host := strings.ToLower(u.Host)

	entry := rc.lookup(host)
	if entry == nil {
		// Nothing cached: do not start work on an already-cancelled context,
		// and do not store an outcome caused by our own cancellation — that
		// is not a property of the host.
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		entry = rc.loadRobots(ctx, host)
		if ctx.Err() == nil {
			rc.store(host, entry)
		}
	}

	if entry.data == nil {
		// Fail-open or no robots.txt: no restrictions.
		return true, nil
	}
	return entry.data.FindGroup(rc.ua).Test(u.Path), nil
}

// CrawlDelay returns the Crawl-delay declared in the host's robots.txt for
// the MCP user-agent (REQ-013). It returns 0 when the host has no robots.txt,
// the file failed to load (fail-open), no Crawl-delay is declared, or the
// host was never visited.
//
// It is consult-only: it reads the cached entry and never fetches. Call
// Allowed first (or rely on a previous crawl of the host) to populate the
// cache before consulting the delay.
func (rc *RobotsClient) CrawlDelay(host string) time.Duration {
	entry := rc.lookup(strings.ToLower(host))
	if entry == nil || entry.data == nil {
		return 0
	}
	return entry.data.FindGroup(rc.ua).CrawlDelay
}

// Close releases the in-memory per-host cache. The client remains usable;
// a subsequent Allowed re-fetches robots.txt.
func (rc *RobotsClient) Close() {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.entries = make(map[string]*robotsEntry)
}

// lookup returns the cached entry for host, or nil if absent.
func (rc *RobotsClient) lookup(host string) *robotsEntry {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.entries[host]
}

// store records the entry for host.
func (rc *RobotsClient) store(host string, entry *robotsEntry) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.entries[host] = entry
}

// loadRobots fetches and parses https://<host>/robots.txt (falling back to
// http) and always returns a non-nil entry: on any failure it fails open
// (allowed) per REQ-013 / DECISION-SPEC-003 instead of blocking.
//
// Status handling is done here on purpose: temoto/robotstxt's status helpers
// treat 5xx as disallow-all (fail-closed), which REQ-013 forbids. We classify
// status codes ourselves:
//
//	2xx           → parse the body; unparseable → Warn + fail-open
//	4xx           → no robots.txt (RFC 9309); Info, no restrictions
//	anything else → Warn + fail-open
func (rc *RobotsClient) loadRobots(ctx context.Context, host string) *robotsEntry {
	body, status, err := rc.fetchRobotsTxt(ctx, host)
	if err != nil {
		// Network/transport failure: unreachable → fail-open.
		rc.log.Warn("robots.txt unreachable, proceeding (fail-open)",
			"host", host, "error", err)
		return &robotsEntry{err: err, ts: time.Now()}
	}

	switch {
	case status >= 200 && status < 300:
		data, perr := robotstxt.FromBytes(body)
		if perr != nil {
			// Malformed → fail-open.
			rc.log.Warn("robots.txt malformed, proceeding (fail-open)",
				"host", host, "error", perr)
			return &robotsEntry{err: perr, ts: time.Now()}
		}
		return &robotsEntry{data: data, ts: time.Now()}
	case status >= 400 && status < 500:
		// 4xx: host publishes no robots.txt → no restrictions.
		rc.log.Info("no robots.txt for host", "host", host, "status", status)
		return &robotsEntry{ts: time.Now()}
	default:
		// 5xx (and any unexpected status): unavailable → fail-open.
		rc.log.Warn("robots.txt unavailable, proceeding (fail-open)",
			"host", host, "status", status)
		return &robotsEntry{
			err: fmt.Errorf("robots.txt HTTP status %d", status),
			ts:  time.Now(),
		}
	}
}

// fetchRobotsTxt downloads https://<host>/robots.txt and falls back to
// http://<host>/robots.txt when the HTTPS request fails at the transport
// level (e.g. no TLS listener on 443). It returns the (possibly truncated)
// body, the HTTP status, and any error. The body is capped at
// maxRobotsTxtSize to keep memory bounded (NFR-002).
func (rc *RobotsClient) fetchRobotsTxt(ctx context.Context, host string) ([]byte, int, error) {
	attempt := func(scheme string) ([]byte, int, error) {
		u := &url.URL{Scheme: scheme, Host: host, Path: "/robots.txt"}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("User-Agent", rc.ua)

		resp, err := rc.client.Do(req)
		if err != nil {
			return nil, 0, err
		}
		defer func() { _ = resp.Body.Close() }()

		limited := io.LimitReader(resp.Body, maxRobotsTxtSize+1)
		body, err := io.ReadAll(limited)
		if err != nil {
			return nil, resp.StatusCode, err
		}
		return body, resp.StatusCode, nil
	}

	body, status, err := attempt("https")
	if err == nil {
		return body, status, nil
	}
	// Fall back to plain HTTP only when the HTTPS attempt failed at the
	// transport level (Do returned an error, e.g. connection refused/timeout,
	// TLS handshake failure). Status-code results are returned as-is.
	body, status, httpErr := attempt("http")
	if httpErr != nil {
		return nil, 0, fmt.Errorf("robots.txt https: %v; http fallback: %v", err, httpErr)
	}
	return body, status, nil
}
