package crawl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"

	"nanuq-searcher-mcp/internal/domain"
	"nanuq-searcher-mcp/internal/fetch"
)

// Crawl defaults for zero-valued CrawlConfig fields. They mirror the
// internal/config defaults (DSG-010 code-first) so the crawl core stays
// decoupled from the config layer; the tool layer (TASK-013) may pass its own
// values.
const (
	defaultWorkers  = 8
	defaultMaxPages = 100
	defaultMaxDepth = 3
	defaultTimeout  = 15 // seconds (EC-006 allows up to 15s)
	// defaultUA is the identifiable MCP user agent (NFR-003). It MUST be the
	// same string the fetch tool sends so hosts that tailor robots.txt per
	// agent match the right group (REQ-013).
	defaultUA = "nanuq-mcp/0.1 (+https://github.com/srDino/nanuq-sercher)"
)

// CrawlConfig configures one crawl run (REQ-011/012, EC-006). Zero numeric and
// string fields fall back to the package defaults above. The two booleans are
// explicit — a false zero value is a false request: the tool layer (TASK-013)
// passes true per the REQ-011/REQ-013 defaults.
type CrawlConfig struct {
	Workers       int    // concurrent fetch workers; default 8 (REQ-012)
	MaxPages      int    // page cap; default 100 (RSK-004)
	MaxDepth      int    // BFS depth cap; default 3
	SameHost      bool   // stay on the root host (REQ-011)
	RespectRobots bool   // enforce robots.txt (REQ-013)
	TimeoutSec    int    // per-request timeout in seconds; default 15 (EC-006)
	UserAgent     string // UA for robots and fetches; default the MCP UA (NFR-003)
}

// withDefaults fills zero numeric/string fields of cfg with the package
// defaults.
func (c CrawlConfig) withDefaults() CrawlConfig {
	if c.Workers <= 0 {
		c.Workers = defaultWorkers
	}
	if c.MaxPages <= 0 {
		c.MaxPages = defaultMaxPages
	}
	if c.MaxDepth <= 0 {
		c.MaxDepth = defaultMaxDepth
	}
	if c.TimeoutSec <= 0 {
		c.TimeoutSec = defaultTimeout
	}
	if c.UserAgent == "" {
		c.UserAgent = defaultUA
	}
	return c
}

// job is one frontier entry: a URL to visit and its BFS depth (root = 0).
type job struct {
	url   string
	depth int
}

// crawler runs one crawl: a BFS frontier consumed by a worker pool
// (REQ-011/012). All shared state lives behind c.mu; workers pull jobs from a
// slice-backed queue guarded by a sync.Cond, which lets the pool terminate
// deterministically once the frontier drains. Per-host pacing (Crawl-delay,
// REQ-013) uses a lazily-created per-host mutex so requests to one host are
// serialized and spaced apart.
type crawler struct {
	ctx    context.Context
	cfg    CrawlConfig
	log    *slog.Logger
	robots *RobotsClient
	fetch  *fetch.Client
	root   string // normalized root URL (SameHost policy anchor)

	mu        sync.Mutex
	cond      *sync.Cond
	queue     []job
	head      int
	inFlight  int
	seen      map[string]bool   // normalized URL dedup
	blocked   map[string]string // host → HostErrors message (429/5xx stop)
	visited   int               // pages actually fetched (cap accounting)
	pages     []domain.Page     // results in visit order
	done      bool              // frontier drained, workers may exit
	cancelled bool              // ctx cancelled mid-crawl

	hostLocksMu sync.Mutex
	hostLocks   map[string]*sync.Mutex
}

// logSafeURL reduces a raw URL to its scheme://host+path for logging
// (DSG-012): query strings, fragments and userinfo are stripped so sensitive
// tokens (API keys, session ids, ...) never reach the logs. Unparseable
// input degrades to "<invalid>" instead of leaking the raw string.
func logSafeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<invalid>"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// Crawl runs a BFS crawl of rootURL (REQ-011/012/013, EC-006). It returns a
// *domain.SiteMap — partial, with Cancelled=true, when ctx is cancelled
// mid-crawl. Crawl never returns nil.
//
// The crawl is a breadth-first frontier consumed by a pool of cfg.Workers
// workers. URLs are deduplicated by NormalizeURL; robots.txt is enforced per
// URL (fail-open, REQ-013); Crawl-delay paces same-host requests; hosts that
// answer 429/5xx are recorded in HostErrors and skipped for the rest of the
// run; per-page fetch/parse failures land in Page.Errors without aborting the
// crawl. Page.Content is left empty — include_content is resolved by TASK-013.
func Crawl(ctx context.Context, rootURL string, cfg CrawlConfig, logger *slog.Logger) *domain.SiteMap {
	cfg = cfg.withDefaults()
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	root, err := NormalizeURL(rootURL)
	if err != nil {
		// Log only the sanitized scheme://host+path (DSG-012): the raw input
		// may carry a query string with sensitive tokens, and the error from
		// NormalizeURL embeds the raw URL too, so it is intentionally omitted.
		logger.Error("crawl: invalid root URL", "url", logSafeURL(rootURL))
		return &domain.SiteMap{RootURL: rootURL, HostErrors: map[string]string{}}
	}

	robots := NewRobotsClient(RobotsConfig{
		UserAgent:   cfg.UserAgent, // same UA as fetch → robots group matches (REQ-013)
		HTTPTimeout: time.Duration(cfg.TimeoutSec) * time.Second,
	}, logger)
	defer robots.Close()

	// One shared fetch client for the whole run. MaxBytes and MaxRedirects
	// are not part of CrawlConfig, so they use the fetch defaults (2 MiB / 5);
	// the tool layer can wire its own values in TASK-013.
	fc, err := fetch.New(fetch.Config{
		TimeoutSec: cfg.TimeoutSec,
		UserAgent:  cfg.UserAgent,
	})
	if err != nil {
		logger.Error("crawl: build fetch client", "error", err)
		return &domain.SiteMap{RootURL: root, HostErrors: map[string]string{}}
	}

	c := &crawler{
		ctx:       ctx,
		cfg:       cfg,
		log:       logger,
		robots:    robots,
		fetch:     fc,
		root:      root,
		queue:     make([]job, 0, 64),
		seen:      make(map[string]bool),
		blocked:   make(map[string]string),
		hostLocks: make(map[string]*sync.Mutex),
	}
	c.cond = sync.NewCond(&c.mu)

	// Seed the frontier with the root. When the context is already done the
	// root is not queued: mark the frontier done so the workers exit instead
	// of waiting forever.
	seeded := c.enqueue(job{url: root, depth: 0})
	if !seeded {
		c.mu.Lock()
		c.done = true
		c.cond.Broadcast()
		c.mu.Unlock()
	}

	var wg sync.WaitGroup
	for i := 0; i < cfg.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.worker()
		}()
	}
	wg.Wait()

	c.mu.Lock()
	defer c.mu.Unlock()
	return &domain.SiteMap{
		RootURL:    root,
		Pages:      c.pages,
		Visited:    c.visited,
		Cancelled:  c.cancelled || ctx.Err() != nil,
		HostErrors: c.blocked,
	}
}

// worker pulls jobs off the frontier until it drains or the crawl is done.
func (c *crawler) worker() {
	for {
		c.mu.Lock()
		for !c.done && c.head == len(c.queue) {
			c.cond.Wait()
		}
		if c.done {
			c.mu.Unlock()
			return
		}
		j := c.queue[c.head]
		c.head++
		c.inFlight++
		c.mu.Unlock()

		c.visit(j)

		c.mu.Lock()
		c.inFlight--
		if c.inFlight == 0 && c.head == len(c.queue) {
			c.done = true
			c.cond.Broadcast()
		}
		c.mu.Unlock()
	}
}

// enqueue adds a job to the frontier after normalization, dedup and limit
// checks. It is safe to call concurrently and returns false when the job was
// not queued (context cancelled, already seen, cap reached, blocked host, out
// of depth).
func (c *crawler) enqueue(j job) bool {
	if c.ctx.Err() != nil {
		return false
	}
	u, err := NormalizeURL(j.url)
	if err != nil {
		return false
	}
	j.url = u

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.ctx.Err() != nil {
		return false
	}
	if c.seen[j.url] {
		return false
	}
	if j.depth > c.cfg.MaxDepth {
		return false
	}
	if c.visited >= c.cfg.MaxPages {
		return false
	}
	if host, err := hostKey(j.url); err == nil {
		if _, blocked := c.blocked[host]; blocked {
			return false
		}
	}

	c.seen[j.url] = true
	c.queue = append(c.queue, j)
	c.cond.Signal()
	return true
}

// visit fetches and parses one job: robots gate, per-host pacing, GET,
// extraction, and enqueueing of the page's links.
func (c *crawler) visit(j job) {
	page := domain.Page{URL: j.url, Depth: j.depth}

	host, err := hostKey(j.url)
	if err != nil {
		page.Errors = append(page.Errors, err.Error())
		c.record(page)
		return
	}
	// Host-level stop (429/5xx): never visit more URLs of a blocked host.
	if c.hostBlocked(host) {
		return
	}

	// Robots.txt gate (REQ-013): skip URLs the host disallows. Allowed fails
	// open (no robots.txt / unreachable / malformed), so err here is either a
	// cancellation or an invalid URL.
	if c.cfg.RespectRobots {
		allowed, err := c.robots.Allowed(c.ctx, j.url)
		if err != nil {
			if c.ctx.Err() != nil {
				c.cancel()
				return
			}
			page.Errors = append(page.Errors, fmt.Sprintf("robots: %v", err))
		} else if !allowed {
			return
		}
	}

	// Per-host pacing: serialize same-host requests and honor the host's
	// Crawl-delay (REQ-013) between consecutive requests to that host.
	hostMu := c.hostLock(host)
	hostMu.Lock()
	if delay := c.robots.CrawlDelay(host); delay > 0 {
		t := time.NewTimer(delay)
		select {
		case <-c.ctx.Done():
			t.Stop()
			hostMu.Unlock()
			c.cancel()
			return
		case <-t.C:
		}
	}
	// Reserve the visited slot atomically: the cap may have been reached while
	// this job waited in the queue (robots check / pacing). A dropped job is
	// neither counted nor recorded, so Visited never exceeds MaxPages and
	// never counts pages that robots.txt or a blocked host filtered out.
	c.mu.Lock()
	if c.visited >= c.cfg.MaxPages {
		c.mu.Unlock()
		hostMu.Unlock()
		return
	}
	c.visited++
	c.mu.Unlock()

	resp, err := c.fetch.Get(c.ctx, j.url)
	hostMu.Unlock()

	if err != nil {
		// 429/5xx mark the host as hostile (EC-006): record it and stop
		// visiting any further URL of that host.
		var he *fetch.HTTPError
		if errors.As(err, &he) && (he.StatusCode >= 500 || he.StatusCode == http.StatusTooManyRequests) {
			c.blockHost(host, fmt.Sprintf("HTTP %d %s", he.StatusCode, he.Status))
			page.Errors = append(page.Errors, err.Error())
			c.record(page)
			return
		}
		// Cancellation aborts the crawl (partial result); anything else is a
		// per-page failure that must not abort the run. The page is recorded
		// in both cases so Visited stays consistent with Pages.
		if c.ctx.Err() != nil {
			c.cancel()
		}
		page.Errors = append(page.Errors, err.Error())
		c.record(page)
		return
	}

	title, headings, links, perr := extractHTML(resp.Body)
	if perr != nil {
		page.Errors = append(page.Errors, fmt.Sprintf("parse: %v", perr))
	}
	page.Title = title
	page.Headings = headings
	// Page.Content intentionally left empty: include_content is TASK-013.
	c.record(page)

	// Relative links resolve against the final URL (redirects may have moved
	// the page); the requested URL is the fallback.
	base, berr := url.Parse(resp.FinalURL)
	if berr != nil {
		base, _ = url.Parse(j.url)
	}
	for _, href := range links {
		c.enqueueLink(j.depth, href, base)
	}
}

// enqueueLink resolves one href found on a page at depth against baseURL,
// applies the link filters (scheme, SameHost, depth cap) and enqueues it at
// depth+1. It is called by visit while processing one page.
func (c *crawler) enqueueLink(depth int, href string, base *url.URL) {
	href = strings.TrimSpace(href)
	if href == "" {
		return
	}
	ref, err := url.Parse(href)
	if err != nil {
		return
	}
	// Filter non-http(s) schemes (mailto:, javascript:, tel:, data:, ...) and
	// fragment-only links before resolving. "" means relative or
	// protocol-relative: ResolveReference supplies the scheme from base.
	switch strings.ToLower(ref.Scheme) {
	case "", "http", "https":
	default:
		return
	}
	abs := base.ResolveReference(ref)
	if abs.Scheme != "http" && abs.Scheme != "https" {
		return
	}

	// Same-host policy (REQ-011): stay on the root host unless disabled.
	if c.cfg.SameHost && !IsSameHost(abs.String(), c.root) {
		return
	}
	// Depth cap: only enqueue links that stay within MaxDepth.
	if depth+1 > c.cfg.MaxDepth {
		return
	}
	c.enqueue(job{url: abs.String(), depth: depth + 1})
}

// extractHTML pulls the <title>, H1..H6 headings and <a href> links out of an
// HTML body using golang.org/x/net/html. Headings and the title are captured
// as their visible text (concatenated descendant text nodes, trimmed); links
// keep the raw href for later resolution against the page's final URL. The
// body is assumed to be (at least mostly) UTF-8 — charset conversion to UTF-8
// is the conversion stage's job (TASK-009); the crawler sees the raw bytes.
func extractHTML(body []byte) (title string, headings []domain.Heading, links []string, err error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return "", nil, nil, err
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "title":
				if title == "" {
					title = textOf(n)
				}
			case "a":
				if href := attrOf(n, "href"); href != "" {
					links = append(links, href)
				}
			}
			if len(n.Data) == 2 && n.Data[0] == 'h' && n.Data[1] >= '1' && n.Data[1] <= '6' {
				headings = append(headings, domain.Heading{Level: int(n.Data[1] - '0'), Text: textOf(n)})
			}
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(doc)
	return title, headings, links, nil
}

// attrOf returns the value of the first attribute with key key (keys are
// lowercased by the tokenizer), or "".
func attrOf(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// textOf concatenates the descendant text nodes of n and trims surrounding
// whitespace.
func textOf(n *html.Node) string {
	var sb strings.Builder
	var collect func(*html.Node)
	collect = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			collect(ch)
		}
	}
	collect(n)
	return strings.TrimSpace(sb.String())
}

// record appends a page to the result list (visit order).
func (c *crawler) record(page domain.Page) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pages = append(c.pages, page)
}

// blockHost records a 429/5xx host stop (first message wins).
func (c *crawler) blockHost(host, msg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.blocked[host]; !exists {
		c.blocked[host] = msg
	}
}

// hostBlocked reports whether host was stopped by a 429/5xx.
func (c *crawler) hostBlocked(host string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, blocked := c.blocked[host]
	return blocked
}

// cancel flags the run as cancelled (partial result).
func (c *crawler) cancel() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancelled = true
}

// hostLock returns the per-host pacing mutex for host, creating it on first
// use. Requests to the same host are serialized through it, which combined
// with the Crawl-delay sleep in visit spaces same-host requests apart.
func (c *crawler) hostLock(host string) *sync.Mutex {
	c.hostLocksMu.Lock()
	defer c.hostLocksMu.Unlock()
	mu, ok := c.hostLocks[host]
	if !ok {
		mu = &sync.Mutex{}
		c.hostLocks[host] = mu
	}
	return mu
}
