// Proxy layer for the outbound HTTP stack (TASK-008, REQ-013): proxy URL
// parsing with the PROXY_PATTERN_MAPPING scheme patterns, per-host proxy
// selection (GetProxyForHost) and SOCKS dialers built on
// golang.org/x/net/proxy. The integration hook is configureProxy in
// client.go, which activates when config.Outgoing.Proxies has entries.
//
// Port fidelity (CON-005): the pattern mapping and per-request selection
// mirror example/searxng/searx/network/network.py (iter_proxies /
// get_proxy_cycles). FASE B selects the first proxy URL of a pattern's
// list; round-robin rotation (itertools.cycle) is FASE C.
package network

import (
	"errors"
	"fmt"
	"net"
	"net/url"

	"golang.org/x/net/proxy"
)

// PROXY_PATTERN_MAPPING normalizes outgoing.proxies keys to canonical
// "scheme://" form. Port of example/searxng/searx/network/network.py
// PROXY_PATTERN_MAPPING (the bare and trailing-colon variants of
// http/https/socks4/socks5/socks5h). Exported for port fidelity (CON-005);
// treat it as read-only.
var PROXY_PATTERN_MAPPING = map[string]string{
	"http":     "http://",
	"https":    "https://",
	"socks4":   "socks4://",
	"socks5":   "socks5://",
	"socks5h":  "socks5h://",
	"http:":    "http://",
	"https:":   "https://",
	"socks4:":  "socks4://",
	"socks5:":  "socks5://",
	"socks5h:": "socks5h://",
}

// normalizeProxyPattern maps a config key to its canonical "scheme://"
// pattern. "all" and "all:" are the wildcard pattern (all://), mirroring
// the way firstProxyURL treats the bare "all" key.
func normalizeProxyPattern(key string) string {
	if mapped, ok := PROXY_PATTERN_MAPPING[key]; ok {
		return mapped
	}
	switch key {
	case "all", "all:":
		return "all://"
	}
	return key
}

// ParseProxyURL splits a proxy URL into scheme and host
// ("socks5://127.0.0.1:9050" → ("socks5", "127.0.0.1:9050")). The accepted
// schemes are those of PROXY_PATTERN_MAPPING plus the wildcard "all://".
// "all://" is a multi-proxy wildcard pattern without a specific host, so
// its host may be empty; every other scheme requires a non-empty host.
// An empty raw string and unknown schemes are errors.
func ParseProxyURL(raw string) (scheme, host string, err error) {
	if raw == "" {
		return "", "", errors.New("network: empty proxy URL")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("network: parse proxy URL %q: %w", raw, err)
	}
	switch u.Scheme {
	case "all":
		return "all", u.Host, nil
	case "socks4", "socks5", "socks5h", "http", "https":
		if u.Host == "" {
			return "", "", fmt.Errorf("network: proxy URL %q has no host", raw)
		}
		return u.Scheme, u.Host, nil
	default:
		return "", "", fmt.Errorf("network: unsupported proxy scheme %q in %q", u.Scheme, raw)
	}
}

// GetProxyForHost selects the proxy that applies to rawURL according to the
// outgoing.proxies pattern map (network.py iter_proxies + httpx mount
// semantics): a scheme pattern ("https://") matches targets of that scheme
// and wins over the "all://" wildcard, which matches any target not covered
// by a more specific pattern. It returns the first non-empty proxy URL of
// the matching pattern's list; the second return value is false when no
// pattern matches (direct connection). Round-robin rotation of the list
// (get_proxy_cycles / itertools.cycle) is FASE C, not FASE B.
func GetProxyForHost(rawURL string, proxies map[string][]string) (string, bool) {
	if len(proxies) == 0 {
		return "", false
	}
	target, err := url.Parse(rawURL)
	if err != nil || target.Scheme == "" {
		return "", false
	}
	// First pass: a scheme-specific pattern for the target scheme.
	for key, list := range proxies {
		if normalizeProxyPattern(key) == target.Scheme+"://" {
			if raw, ok := firstProxy(list); ok {
				return raw, true
			}
		}
	}
	// Fallback: the all:// wildcard applies to any target.
	for key, list := range proxies {
		if normalizeProxyPattern(key) == "all://" {
			if raw, ok := firstProxy(list); ok {
				return raw, true
			}
		}
	}
	return "", false
}

// firstProxy returns the first non-empty proxy URL of a pattern's list
// (network.py wraps a single proxy string in a list; a list holds the
// rotation candidates).
func firstProxy(list []string) (string, bool) {
	for _, p := range list {
		if p != "" {
			return p, true
		}
	}
	return "", false
}

// BuildDialer builds a proxy.Dialer for a SOCKS proxy URL (REQ-013). The
// dialer connects to proxyURL's host and tunnels target connections through
// it; it performs no I/O at build time.
//
// SOCKS5 semantics: golang.org/x/net/proxy always resolves the target
// hostname on the proxy side (remote DNS), so both "socks5://" and
// "socks5h://" behave like the Python client's socks5h (the "h" stands for
// hostname resolution on the proxy — network.py get_transport_for_socks
// strips socks5h:// to socks5:// + rdns=True). There is no SOCKS4 client in
// golang.org/x/net/proxy, so "socks4://" is rejected — the REQ-013 SOCKS4
// leg is deferred (documented deviation).
//
// HTTP(S) proxies parse fine but are rejected here: TASK-008 covers SOCKS
// only, and http/https proxies are handled by the per-request
// Transport.Proxy hook in configureProxy instead.
func BuildDialer(proxyURL string) (proxy.Dialer, error) {
	return buildDialer(proxyURL, nil)
}

// buildDialer is BuildDialer with an explicit forward dialer.
// configureProxy uses it to compose the source-IP dialer
// (outgoing.source_ips hook) as the first hop to the SOCKS proxy; a nil
// forward dials the proxy directly.
func buildDialer(proxyURL string, forward proxy.Dialer) (proxy.Dialer, error) {
	scheme, host, err := ParseProxyURL(proxyURL)
	if err != nil {
		return nil, err
	}
	switch scheme {
	case "socks5", "socks5h":
		return proxy.SOCKS5("tcp", host, nil, forward)
	case "socks4":
		return nil, errors.New("network: socks4 proxy not supported: golang.org/x/net/proxy has no SOCKS4 client (deferred, REQ-013)")
	case "http", "https":
		return nil, errors.New("network: http proxy not supported in this phase (TASK-008 covers SOCKS only)")
	default:
		return nil, fmt.Errorf("network: unsupported proxy scheme %q", scheme)
	}
}

// SourceIPDialer returns a dialer bound to the given local source IP — the
// source_ips hook (SearXNG outgoing.source_ips IP rotation). The returned
// *net.Dialer satisfies both proxy.Dialer and proxy.ContextDialer. This is
// a hook only: full per-request IP rotation is out of scope for TASK-008.
func SourceIPDialer(ip string) (proxy.Dialer, error) {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return nil, fmt.Errorf("network: invalid source IP %q", ip)
	}
	return &net.Dialer{LocalAddr: &net.TCPAddr{IP: parsed}}, nil
}
