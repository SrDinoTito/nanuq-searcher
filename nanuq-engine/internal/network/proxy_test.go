package network

// Tests for the SOCKS/HTTP proxy layer (TASK-008, REQ-013): proxy URL
// parsing (PROXY_PATTERN_MAPPING), per-host pattern selection, the
// x/net/proxy SOCKS dialer, the source-IP hook and the transport wiring
// configureProxy performs (DSG-006).

import (
	"net/http"
	"net/url"
	"testing"

	"golang.org/x/net/proxy"

	"nanuq-engine/internal/config"
)

// --- TestParseProxyURL (TASK-008): scheme/host splitting of proxy URLs ---
func TestParseProxyURL(t *testing.T) {
	cases := []struct {
		raw        string
		wantScheme string
		wantHost   string
		wantErr    bool
	}{
		{raw: "socks5://host:1080", wantScheme: "socks5", wantHost: "host:1080"},
		{raw: "socks5h://host:1080", wantScheme: "socks5h", wantHost: "host:1080"},
		{raw: "socks4://host:1080", wantScheme: "socks4", wantHost: "host:1080"},
		// http:// parses fine; BuildDialer rejects it in this phase.
		{raw: "http://host:8080", wantScheme: "http", wantHost: "host:8080"},
		{raw: "https://host:8080", wantScheme: "https", wantHost: "host:8080"},
		// all:// is a wildcard pattern without a specific host.
		{raw: "all://", wantScheme: "all", wantHost: ""},
		{raw: "", wantErr: true},
		{raw: "socks5://", wantErr: true},  // missing host
		{raw: "ftp://host", wantErr: true}, // unsupported scheme
	}
	for _, tc := range cases {
		scheme, host, err := ParseProxyURL(tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseProxyURL(%q) = (%q, %q, nil), want error", tc.raw, scheme, host)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseProxyURL(%q) error = %v", tc.raw, err)
			continue
		}
		if scheme != tc.wantScheme || host != tc.wantHost {
			t.Errorf("ParseProxyURL(%q) = (%q, %q), want (%q, %q)", tc.raw, scheme, host, tc.wantScheme, tc.wantHost)
		}
	}
}

// --- TestGetProxyForHost (TASK-008): scheme-pattern and all:// selection ---
func TestGetProxyForHost(t *testing.T) {
	// all:// wildcard matches any target scheme.
	all := map[string][]string{"all://": {"socks5://127.0.0.1:9050"}}
	for _, target := range []string{"https://example.com/", "http://example.com/"} {
		if got, ok := GetProxyForHost(target, all); !ok || got != "socks5://127.0.0.1:9050" {
			t.Errorf("GetProxyForHost(%q, all://) = (%q, %v), want (socks5://127.0.0.1:9050, true)", target, got, ok)
		}
	}

	// https:// pattern matches only https targets.
	scheme := map[string][]string{"https://": {"socks5://127.0.0.1:1080"}}
	if got, ok := GetProxyForHost("https://example.com/", scheme); !ok || got != "socks5://127.0.0.1:1080" {
		t.Errorf("GetProxyForHost(https, https://) = (%q, %v), want (socks5://127.0.0.1:1080, true)", got, ok)
	}
	if got, ok := GetProxyForHost("http://example.com/", scheme); ok {
		t.Errorf("GetProxyForHost(http, https://) = (%q, %v), want no match", got, ok)
	}

	// A scheme-specific pattern wins over the all:// wildcard.
	mixed := map[string][]string{
		"all://":   {"socks5://wild:9050"},
		"https://": {"socks5://scheme:1080"},
	}
	if got, _ := GetProxyForHost("https://example.com/", mixed); got != "socks5://scheme:1080" {
		t.Errorf("GetProxyForHost(https, mixed) = %q, want scheme pattern to win over all://", got)
	}
	if got, _ := GetProxyForHost("http://example.com/", mixed); got != "socks5://wild:9050" {
		t.Errorf("GetProxyForHost(http, mixed) = %q, want all:// fallback", got)
	}

	// Normalized bare key "https" (no ://) works too.
	bare := map[string][]string{"https": {"socks5://127.0.0.1:1080"}}
	if got, ok := GetProxyForHost("https://example.com/", bare); !ok || got != "socks5://127.0.0.1:1080" {
		t.Errorf("GetProxyForHost(https, bare key) = (%q, %v), want match", got, ok)
	}

	// No match / empty config → direct connection.
	if got, ok := GetProxyForHost("http://example.com/", map[string][]string{}); ok {
		t.Errorf("GetProxyForHost(http, empty) = (%q, %v), want no match", got, ok)
	}
	if got, ok := GetProxyForHost("not-a-url", all); ok {
		t.Errorf("GetProxyForHost(unparseable target) = (%q, %v), want no match", got, ok)
	}
}

// --- TestBuildDialerSOCKS5 (TASK-008, REQ-013): dialer construction ---
func TestBuildDialerSOCKS5(t *testing.T) {
	// socks5 and socks5h build a dialer without I/O; nothing connects here.
	for _, raw := range []string{"socks5://127.0.0.1:1080", "socks5h://127.0.0.1:1080"} {
		d, err := BuildDialer(raw)
		if err != nil {
			t.Errorf("BuildDialer(%q) error = %v, want nil", raw, err)
			continue
		}
		if d == nil {
			t.Errorf("BuildDialer(%q) = nil dialer", raw)
		}
	}

	// HTTP(S) proxies parse but are not dialable in this phase.
	for _, raw := range []string{"http://127.0.0.1:3128", "https://127.0.0.1:3128"} {
		if _, err := BuildDialer(raw); err == nil {
			t.Errorf("BuildDialer(%q) = nil error, want phase rejection", raw)
		}
	}

	// SOCKS4 has no client in golang.org/x/net/proxy (documented deviation).
	if _, err := BuildDialer("socks4://127.0.0.1:1080"); err == nil {
		t.Error("BuildDialer(socks4://) = nil error, want rejection")
	}

	// all:// is a pattern, not a dialable proxy.
	if _, err := BuildDialer("all://"); err == nil {
		t.Error("BuildDialer(all://) = nil error, want rejection")
	}
}

// --- TestSourceIPDialer (TASK-008): source_ips hook dialer ---
func TestSourceIPDialer(t *testing.T) {
	d, err := SourceIPDialer("192.0.2.10")
	if err != nil {
		t.Fatalf("SourceIPDialer(valid) error = %v", err)
	}
	if _, ok := d.(proxy.ContextDialer); !ok {
		t.Error("SourceIPDialer dialer does not implement proxy.ContextDialer")
	}

	if _, err := SourceIPDialer("not-an-ip"); err == nil {
		t.Error("SourceIPDialer(invalid) = nil error, want error")
	}
}

// --- TestTransportSocksDialContext (TASK-008, DSG-006): SOCKS hook wiring ---
func TestTransportSocksDialContext(t *testing.T) {
	for _, src := range [][]string{nil, {"192.0.2.10"}} {
		c, err := New(&config.Config{
			Outgoing: config.Outgoing{
				Proxies:   map[string][]string{"all://": {"socks5://127.0.0.1:9050"}},
				SourceIPs: src,
			},
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		tr, ok := c.getClient().Transport.(*http.Transport)
		if !ok {
			t.Fatalf("Transport type = %T, want *http.Transport", c.getClient().Transport)
		}
		if tr.DialContext == nil {
			t.Error("SOCKS config: DialContext is nil, want the x/net/proxy dialer")
		}
		if tr.Proxy != nil {
			t.Error("SOCKS config: Proxy callback set, want nil (SOCKS uses DialContext)")
		}
	}
}

// --- TestTransportHTTPProxyHook (TASK-008): per-request CONNECT selection ---
func TestTransportHTTPProxyHook(t *testing.T) {
	c, err := New(&config.Config{
		Outgoing: config.Outgoing{
			Proxies: map[string][]string{"https://": {"http://127.0.0.1:3128"}},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr, ok := c.getClient().Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport type = %T, want *http.Transport", c.getClient().Transport)
	}
	if tr.Proxy == nil {
		t.Fatal("http proxy config: Proxy callback is nil")
	}

	u, err := tr.Proxy(&http.Request{URL: mustURL(t, "https://example.com/")})
	if err != nil {
		t.Fatalf("Proxy(https target) error = %v", err)
	}
	if u == nil || u.String() != "http://127.0.0.1:3128" {
		t.Errorf("Proxy(https target) = %v, want http://127.0.0.1:3128", u)
	}

	u, err = tr.Proxy(&http.Request{URL: mustURL(t, "http://example.com/")})
	if err != nil {
		t.Fatalf("Proxy(http target) error = %v", err)
	}
	if u != nil {
		t.Errorf("Proxy(http target) = %v, want nil (no pattern match → direct)", u)
	}
}

// mustURL parses raw and fails the test on error.
func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return u
}
