package crawl

import (
	"fmt"
	"net/url"
	"strings"
)

// NormalizeURL canonicalizes an absolute URL for deduplication and host
// comparison (TASK-012, REQ-011/012): the scheme and host are lowercased, the
// default port for the scheme (80 for http, 443 for https) is removed, the
// fragment is dropped and the query string is preserved. The path is left
// untouched (including case and a trailing slash).
//
// The input must be absolute: an http/https scheme and a non-empty host are
// required, anything else is an error. Relative or scheme-less URLs (which
// never reach this function from the crawler — links are resolved against
// their page's final URL first) are rejected.
func NormalizeURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("normalize: parse %q: %w", raw, err)
	}
	if u.Scheme == "" {
		return "", fmt.Errorf("normalize: %q: missing scheme", raw)
	}
	if u.Host == "" {
		return "", fmt.Errorf("normalize: %q: missing host", raw)
	}

	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Host)

	// Strip the default port for the scheme (RFC 3986 §3.2.3). Non-default
	// ports (e.g. http on 8080, or http on 443) are kept: they identify a
	// different service.
	if p := u.Port(); p != "" {
		if (scheme == "http" && p == "80") || (scheme == "https" && p == "443") {
			host = strings.TrimSuffix(host, ":"+p)
		}
	}

	u.Scheme = scheme
	u.Host = host
	u.Fragment = ""
	return u.String(), nil
}

// IsSameHost reports whether a and b refer to the same host under the
// normalized rules of NormalizeURL: case-insensitive hostname, default ports
// ignored. The scheme is deliberately NOT part of the comparison (http and
// https of one host are the same site for the SameHost policy, REQ-011). A
// URL that fails to parse, or lacks a host, yields false.
func IsSameHost(a, b string) bool {
	ha, err := hostKey(a)
	if err != nil {
		return false
	}
	hb, err := hostKey(b)
	if err != nil {
		return false
	}
	return ha == hb
}

// hostKey reduces an absolute URL to its normalized host identity: the
// lowercased host with the scheme's default port stripped. It is the shared
// comparison key for IsSameHost and the crawler's per-host bookkeeping
// (pacing, HostErrors). It deliberately matches the RobotsClient cache key
// for already-normalized URLs (lowercase host including the effective port).
func hostKey(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Host == "" {
		return "", fmt.Errorf("no host in %q", raw)
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Host)
	if p := u.Port(); p != "" {
		if (scheme == "http" && p == "80") || (scheme == "https" && p == "443") {
			host = strings.TrimSuffix(host, ":"+p)
		}
	}
	return host, nil
}
