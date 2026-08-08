package fetch

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/net/html/charset"
)

// Defaults (DSG-010: code-first config defaults).
const (
	defaultTimeoutSec   = 30
	defaultMaxBytes     = 2 << 20 // 2 MiB
	defaultMaxRedirects = 5
	defaultUserAgent    = "nanuq-mcp/0.1 (+https://github.com/srDino/nanuq-sercher)"
)

// Config configures the fetch Client. Zero values fall back to the DSG-010
// defaults. The max_bytes valid range (64 KiB..10 MiB, REQ-008) is validated
// at the tool layer, not here.
type Config struct {
	TimeoutSec   int    // per-request timeout in seconds; default 30 (REQ-010)
	MaxBytes     int64  // body limit in bytes; default 2 MiB (REQ-008)
	MaxRedirects int    // max redirects to follow; default 5 (EC-003)
	UserAgent    string // identifiable UA (NFR-003); default "nanuq-mcp/0.1 (+...)"
}

// Client is a hardened HTTP client enforcing the REQ-010 guardrails
// (DSG-006 step 1): http/https only, per-request timeout, bounded redirects,
// HTML-only Content-Type, charset detection and max_bytes truncation.
type Client struct {
	http         *http.Client
	maxBytes     int64
	maxRedirects int
	userAgent    string
}

// New validates cfg and builds a Client, applying the DSG-010 defaults for
// zero-valued fields. Negative values are rejected as configuration bugs.
func New(cfg Config) (*Client, error) {
	switch {
	case cfg.TimeoutSec < 0:
		return nil, fmt.Errorf("fetch: TimeoutSec must be >= 0, got %d", cfg.TimeoutSec)
	case cfg.MaxBytes < 0:
		return nil, fmt.Errorf("fetch: MaxBytes must be >= 0, got %d", cfg.MaxBytes)
	case cfg.MaxRedirects < 0:
		return nil, fmt.Errorf("fetch: MaxRedirects must be >= 0, got %d", cfg.MaxRedirects)
	}
	if cfg.TimeoutSec == 0 {
		cfg.TimeoutSec = defaultTimeoutSec
	}
	if cfg.MaxBytes == 0 {
		cfg.MaxBytes = defaultMaxBytes
	}
	if cfg.MaxRedirects == 0 {
		cfg.MaxRedirects = defaultMaxRedirects
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = defaultUserAgent
	}

	c := &Client{
		maxBytes:     cfg.MaxBytes,
		maxRedirects: cfg.MaxRedirects,
		userAgent:    cfg.UserAgent,
	}
	c.http = &http.Client{
		Timeout:       time.Duration(cfg.TimeoutSec) * time.Second,
		CheckRedirect: c.checkRedirect,
	}
	return c, nil
}

// Response is the result of a successful fetch: the body (already limited to
// MaxBytes) plus the metadata the pipeline needs (DSG-006 step 1). Charset is
// detected and exposed here, but the actual decoding to UTF-8 is performed
// downstream by the conversion stage (TASK-009).
type Response struct {
	URL         string // final URL after redirects
	StatusCode  int
	ContentType string
	Charset     string // detected charset name, or "" when not applicable
	Body        []byte // limited to MaxBytes
	Truncated   bool   // true if Body was cut at MaxBytes
	FinalURL    string // final URL after redirects
}

// Get fetches rawURL following the REQ-010 guardrails. It returns a
// descriptive error for invalid URLs, non-http/https schemes
// (ErrUnsupportedScheme), redirect policy violations (ErrTooManyRedirects),
// timeouts, non-2xx statuses (*HTTPError) and non-HTML responses
// (ErrNotHTML / *NotHTMLError). Network errors are wrapped with the URL for
// context. Retry policy is deliberately NOT handled here — it belongs to the
// crawler (TASK-012).
func (c *Client) Get(ctx context.Context, rawURL string) (*Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("fetch %q: invalid URL: %w", rawURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("fetch %q: %w: scheme %q", rawURL, ErrUnsupportedScheme, u.Scheme)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("fetch %q: %w", rawURL, err)
	}
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %q: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("fetch %q: %w", rawURL, &HTTPError{StatusCode: resp.StatusCode, Status: resp.Status})
	}

	contentType := resp.Header.Get("Content-Type")
	mediaType, _, _ := mime.ParseMediaType(contentType)

	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("fetch %q: reading body: %w", rawURL, err)
	}
	truncated := int64(len(body)) > c.maxBytes
	if truncated {
		body = body[:c.maxBytes]
	}

	if mediaType != "text/html" && mediaType != "application/xhtml+xml" {
		// REQ-010/EC-004: reject non-HTML responses. Pragmatic decision
		// (documented): some servers omit the Content-Type header entirely;
		// if the body looks like HTML, accept it as HTML.
		if contentType != "" || !looksLikeHTML(body) {
			return nil, &NotHTMLError{ContentType: contentType}
		}
	}

	finalURL := resp.Request.URL.String()
	return &Response{
		URL:         finalURL,
		StatusCode:  resp.StatusCode,
		ContentType: contentType,
		Charset:     detectCharset(body, contentType),
		Body:        body,
		Truncated:   truncated,
		FinalURL:    finalURL,
	}, nil
}

// checkRedirect implements the redirect policy (EC-003, NFR-003): follow at
// most MaxRedirects redirects, and only to http/https targets. It is invoked
// by http.Client before each redirected request; errors are returned by Get
// wrapped in a *url.Error and unwrap correctly via errors.Is/errors.As.
func (c *Client) checkRedirect(req *http.Request, via []*http.Request) error {
	// via includes the original request, so the first redirect sees
	// len(via) == 1: allow up to MaxRedirects redirects, reject the next.
	if len(via) > c.maxRedirects {
		return fmt.Errorf("%w after %d redirects", ErrTooManyRedirects, c.maxRedirects)
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return fmt.Errorf("%w: redirect to %q (scheme %q)", ErrUnsupportedScheme, req.URL.String(), req.URL.Scheme)
	}
	return nil
}

// detectCharset sniffs the response charset using the WHATWG HTML encoding
// algorithm (golang.org/x/net/html/charset): BOM, then the Content-Type
// charset parameter, then a <meta> prescan over the first 1024 bytes, then a
// UTF-8 heuristic, defaulting to windows-1252 (REQ-010/EC-005). It only
// exposes the name — the actual UTF-8 conversion happens in TASK-009.
func detectCharset(body []byte, contentType string) string {
	if len(body) == 0 {
		return ""
	}
	_, name, _ := charset.DetermineEncoding(body, contentType)
	return name
}

// looksLikeHTML is the pragmatic heuristic for servers that omit the
// Content-Type header: accept the body as HTML when its first non-whitespace
// byte is '<'.
func looksLikeHTML(body []byte) bool {
	for _, b := range body {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		}
		return b == '<'
	}
	return false
}
