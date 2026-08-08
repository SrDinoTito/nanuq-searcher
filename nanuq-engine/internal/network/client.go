// Package network implements the outbound HTTP layer of nanuq-server
// (TASK-007, DSG-006): a transport-caching HTTP client, TLS cipher
// shuffling (CON-003), SOCKS/HTTP proxy support (TASK-008, REQ-013) and
// HTTP error classification that feeds the engine suspension mechanism
// (REQ-008).
//
// The package is engine-agnostic: it executes engine.RequestParams and
// implements search.Requester (the seam processor.go uses), but never
// imports internal/engines (RISK-001).
package network

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"golang.org/x/net/http2"
	"golang.org/x/net/proxy"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
)

// Client executes engine.RequestParams over a cached http.Client. One
// transport is built per distinct (proxy, verify, http2, local_addr)
// combination derived from config.Outgoing and reused for the lifetime of
// the Client (DSG-006): no per-request dialing, no sync.Pool, no transport
// retries — the only retry is the single RemoteProtocolError replay in Do
// (REQ-012).
//
// Client is safe for concurrent use by multiple search goroutines.
type Client struct {
	cfg    *config.Outgoing
	logger *slog.Logger

	mu      sync.Mutex
	clients map[string]*http.Client
}

// New builds a Client from the root config (outgoing section). A nil cfg
// is rejected rather than dereferenced. The transport cache is empty until
// the first request; New performs no I/O.
func New(cfg *config.Config) (*Client, error) {
	if cfg == nil {
		return nil, errors.New("network: nil config")
	}
	return &Client{
		cfg:     &cfg.Outgoing,
		logger:  slog.Default(),
		clients: make(map[string]*http.Client),
	}, nil
}

// Do executes the request described by params and returns the HTTP
// response, or an error. It implements search.Requester (asserted in
// network_test.go): errors that wrap engine.EngineSuspendError (429/403
// class, Cloudflare, timeout) mark the engine for suspension (REQ-008).
//
// Body building precedence (engine.RequestParams doc): Data wins over JSON.
// params.Timeout > 0 wraps the caller context with context.WithTimeout —
// nested inside the parent ctx, so the watchdog deadline in the search
// pipeline still applies (DSG-005).
//
// The response body is fully read here so HTTP-level errors can be
// classified (RaiseForHTTPError / Cloudflare detection); it is rewound
// with io.NopCloser before being returned, so engine.Response can consume
// it normally.
func (c *Client) Do(ctx context.Context, params *engine.RequestParams) (*http.Response, error) {
	if params == nil {
		return nil, errors.New("network: nil request params")
	}
	if params.URL == "" {
		return nil, errors.New("network: empty request URL")
	}

	reqCtx := ctx
	cancel := func() {}
	if params.Timeout > 0 {
		reqCtx, cancel = context.WithTimeout(ctx, params.Timeout)
	}
	defer cancel()

	req, err := c.buildRequest(reqCtx, params)
	if err != nil {
		return nil, err
	}

	resp, err := c.getClient().Do(req)
	if err != nil {
		// REQ-012: a remote protocol error (the peer tore the connection
		// mid-exchange) can be transient; drop the cached transport and
		// replay exactly once. Timeouts and other network errors are not
		// retried here — the suspension policy handles them.
		if isProtocolError(err) {
			c.invalidateClient()
			if req2, rerr := c.buildRequest(reqCtx, params); rerr == nil {
				resp, err = c.getClient().Do(req2)
			}
		}
		if err != nil {
			return nil, c.wrapNetworkError(err)
		}
	}

	// Read the body so RaiseForHTTPError can classify Cloudflare /
	// status failures, then rewind it for the engine's Response step.
	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("network: read response body: %w", readErr)
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))

	if herr := RaiseForHTTPError(resp, body); herr != nil {
		return nil, herr
	}
	return resp, nil
}

// buildRequest turns params into an *http.Request bound to ctx. Method
// defaults to GET; Data is form-encoded (application/x-www-form-urlencoded),
// JSON is marshalled (application/json); Headers and Cookies are attached.
func (c *Client) buildRequest(ctx context.Context, params *engine.RequestParams) (*http.Request, error) {
	method := params.Method
	if method == "" {
		method = http.MethodGet
	}

	var body io.Reader
	switch {
	case params.Data != nil:
		form := url.Values{}
		for k, v := range params.Data {
			form.Set(k, v)
		}
		body = strings.NewReader(form.Encode())
	case params.JSON != nil:
		payload, err := json.Marshal(params.JSON)
		if err != nil {
			return nil, fmt.Errorf("network: encode JSON body: %w", err)
		}
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, params.URL, body)
	if err != nil {
		return nil, fmt.Errorf("network: build request: %w", err)
	}
	if params.Data != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if params.JSON != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, vs := range params.Headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	for _, ck := range params.Cookies {
		req.AddCookie(ck)
	}
	return req, nil
}

// Get is a convenience wrapper around Do with the method preset to GET.
// It copies params so the caller's RequestParams.Method is not mutated.
func (c *Client) Get(ctx context.Context, params *engine.RequestParams) (*http.Response, error) {
	p := *params
	p.Method = http.MethodGet
	return c.Do(ctx, &p)
}

// Post is a convenience wrapper around Do with the method preset to POST.
// It copies params so the caller's RequestParams.Method is not mutated.
func (c *Client) Post(ctx context.Context, params *engine.RequestParams) (*http.Response, error) {
	p := *params
	p.Method = http.MethodPost
	return c.Do(ctx, &p)
}

// getClient returns the cached http.Client for the current outgoing
// configuration, building the transport on first use (DSG-006: one
// transport per (proxy, verify, http2, local_addr) combination).
func (c *Client) getClient() *http.Client {
	key := clientKey(c.cfg)
	c.mu.Lock()
	defer c.mu.Unlock()
	if cl, ok := c.clients[key]; ok {
		return cl
	}
	cl := &http.Client{Transport: c.buildTransport()}
	c.clients[key] = cl
	return cl
}

// invalidateClient evicts the cached client so the next getClient builds a
// fresh transport. Used by the REQ-012 replay, where the peer's connection
// state may be wedged.
func (c *Client) invalidateClient() {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.clients, clientKey(c.cfg))
}

// buildTransport builds an *http.Transport from config.Outgoing:
// connection pooling (pool_connections / pool_maxsize), optional HTTP/2
// via golang.org/x/net/http2, TLS verification from outgoing.verify and
// the proxy/source-IP hooks (TASK-008). No retries are configured on the
// transport (REQ-012 retry lives in Do).
func (c *Client) buildTransport() *http.Transport {
	out := c.cfg
	tr := &http.Transport{
		MaxIdleConns:        out.PoolConnections,
		MaxIdleConnsPerHost: out.PoolMaxsize,
		TLSClientConfig:     BuildTLSConfig(verifyTLS(out)),
	}
	c.configureProxy(tr, out)
	if out.EnableHTTP2 {
		if err := http2.ConfigureTransport(tr); err != nil {
			// Transport still works over HTTP/1.1.
			c.logger.Warn("network: http2 setup failed, using http/1.1", "error", err)
		}
	}
	return tr
}

// clientKey derives the transport cache key from the outgoing knobs that
// change the transport: proxy, TLS verification, HTTP/2 and source IP.
func clientKey(out *config.Outgoing) string {
	return strings.Join([]string{
		firstProxyURL(out.Proxies),
		fmt.Sprintf("%v", verifyTLS(out)),
		fmt.Sprintf("%v", out.EnableHTTP2),
		firstSourceIP(out.SourceIPs),
	}, "|")
}

// verifyTLS reports whether TLS certificate verification is enabled.
// outgoing.verify is a FlexString: the YAML boolean false decodes to ""
// (settings.go) and the literal string "false" is accepted too — both mean
// verification off (InsecureSkipVerify).
func verifyTLS(out *config.Outgoing) bool {
	v := string(out.Verify)
	return v != "" && !strings.EqualFold(v, "false")
}

// configureProxy wires the proxy and source-IP hooks into tr (TASK-008,
// REQ-013). The hook activates when config.Outgoing.Proxies has entries;
// without them the transport dials directly (environment proxies are never
// consulted). One transport is built per proxy config (DSG-006): the
// transport-level proxy is the first parseable URL — the same firstProxyURL
// the cache key uses — so cache and transport always agree.
//
// SOCKS proxies ("socks5://", "socks5h://") are tunneled through an
// x/net/proxy dialer installed as tr.DialContext (remote DNS — see
// BuildDialer). HTTP(S) proxies are selected per request through tr.Proxy,
// so GetProxyForHost applies the scheme patterns ("https://") and the
// all:// wildcard per host. An optional source IP (outgoing.source_ips) is
// composed into the SOCKS forward dialer, or used as the CONNECT first-hop
// bind address for HTTP(S) proxies.
//
// FASE B limitation: the transport-level SOCKS proxy is the config's first
// proxy for every request of this transport; per-host SOCKS selection and
// get_proxy_cycles round-robin rotation are FASE C.
func (c *Client) configureProxy(tr *http.Transport, out *config.Outgoing) {
	if len(out.Proxies) == 0 {
		return
	}
	raw := firstProxyURL(out.Proxies)
	if raw == "" {
		return
	}
	scheme, _, err := ParseProxyURL(raw)
	if err != nil {
		c.logger.Warn("network: proxy skipped", "proxy", raw, "error", err)
		return
	}

	// Source-IP hook: compose the local bind address as the forward dialer
	// of the SOCKS proxy, or as the dialer for the CONNECT first hop.
	var forward proxy.Dialer
	if addr := firstSourceIP(out.SourceIPs); addr != "" {
		if d, derr := SourceIPDialer(addr); derr == nil {
			forward = d
		} else {
			c.logger.Warn("network: invalid source_ip ignored", "source_ip", addr, "error", derr)
		}
	}

	switch scheme {
	case "socks5", "socks5h":
		dialer, derr := buildDialer(raw, forward)
		if derr != nil {
			c.logger.Warn("network: socks proxy skipped", "proxy", raw, "error", derr)
			return
		}
		cd, ok := dialer.(proxy.ContextDialer)
		if !ok {
			c.logger.Warn("network: socks dialer lacks DialContext, proxy skipped", "proxy", raw)
			return
		}
		tr.DialContext = cd.DialContext
	case "http", "https":
		if forward != nil {
			if fd, ok := forward.(proxy.ContextDialer); ok {
				tr.DialContext = fd.DialContext
			}
		}
		tr.Proxy = proxyFuncForHost(out.Proxies)
	default:
		c.logger.Warn("network: unsupported proxy scheme skipped", "proxy", raw)
	}
}

// proxyFuncForHost returns the per-request proxy callback: GetProxyForHost
// selects the proxy by target scheme pattern ("https://") with the all://
// wildcard as fallback; a nil *url.URL means direct connection. Used for
// HTTP(S) CONNECT proxies — the SOCKS path uses tr.DialContext.
func proxyFuncForHost(proxies map[string][]string) func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		raw, ok := GetProxyForHost(req.URL.String(), proxies)
		if !ok {
			return nil, nil
		}
		u, err := url.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("network: parse proxy %q: %w", raw, err)
		}
		return u, nil
	}
}

// firstProxyURL picks the first parseable proxy URL from the config map.
// Keys are normalized via PROXY_PATTERN_MAPPING first (network.py
// iter_proxies), so "all://", "all" and "all:" all select the wildcard
// pattern; https wins over http, which wins over all (the precedence the
// bare-key iteration used). Returns "" when nothing parses.
func firstProxyURL(proxies map[string][]string) string {
	for _, scheme := range []string{"https", "http", "all"} {
		want := normalizeProxyPattern(scheme)
		for key, list := range proxies {
			if normalizeProxyPattern(key) != want {
				continue
			}
			for _, p := range list {
				if _, err := url.Parse(p); err == nil {
					return p
				}
			}
		}
	}
	return ""
}

// firstSourceIP returns the first configured source IP, or "".
func firstSourceIP(sourceIPs []string) string {
	if len(sourceIPs) == 0 {
		return ""
	}
	return sourceIPs[0]
}

// isProtocolError reports whether err is a remote-protocol failure worth
// replaying once (REQ-012): an HTTP/2 connection or stream error, or an
// unexpected EOF — the Go counterparts of httpx.RemoteProtocolError.
// Timeouts are deliberately excluded (they are handled by the suspension
// policy, not by retrying).
func isProtocolError(err error) bool {
	var connErr http2.ConnectionError
	if errors.As(err, &connErr) {
		return true
	}
	var streamErr http2.StreamError
	if errors.As(err, &streamErr) {
		return true
	}
	return errors.Is(err, io.ErrUnexpectedEOF)
}

// wrapNetworkError classifies a failed request into a suspension error:
// context/timeout failures become Reason "timeout", everything else is
// wrapped with %w so the pipeline still sees it as suspendable and bans
// with the generic "exception" reason (processor.go HandleException).
func (c *Client) wrapNetworkError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return timeoutSuspendError()
	}
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return timeoutSuspendError()
	}
	return fmt.Errorf("network: request failed: %w", err)
}
