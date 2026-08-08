// Package autocomplete implements the search-query autocompleter backends
// (TASK-013, REQ-019), a Go port of example/searxng/searx/autocomplete.py.
//
// The upstream module exposes 19 backends through a dict; this port ships the
// five backends prioritised by REQ-019 — duckduckgo, google_complete,
// wikipedia, bing and brave — behind a small registry. The upstream
// backends dict (autocomplete.py L393-413) is the source of truth; where this
// port deviates from the Python reference the deviation is documented next
// to the code, mirroring the style used in internal/engines/duckduckgo.go.
//
// Networking: backends build their own standard http.Client with a 5s
// timeout. They intentionally do NOT flow through internal/network yet —
// proxy/TLS/outgoing-config plumbing arrives with TASK-022; see
// httpClient below.
package autocomplete

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ErrUnknownBackend is returned by Search when the requested backend is not
// registered. It mirrors the Python behaviour of returning an empty list for
// unknown names (search_autocomplete L417-419), but as an explicit error so
// the web handler can decide how to degrade.
var ErrUnknownBackend = errors.New("autocomplete: unknown backend")

// Backend is one autocomplete provider: it returns suggestion strings for a
// query. It is the Go counterpart of the Python
// `Callable[[str, str], list[str]]` entries of the backends dict.
type Backend func(ctx context.Context, query string, locale string) ([]string, error)

// httpClient is the shared transport for every backend.
//
// TODO(TASK-022): wire this through internal/network once the outgoing
// configuration (proxy, TLS, request_timeout from settings.yml) is plumbed;
// for now a fixed 5s timeout matches the spirit of
// settings.outgoing.request_timeout (default 3.0 in newConfig) without
// coupling the autocomplete package to the network layer.
var httpClient = &http.Client{Timeout: 5 * time.Second}

// doGet performs a GET and returns the raw response body. customize may
// mutate the request before it is sent (e.g. to set a cookie, as brave
// does). Non-2xx responses are errors; every failure is wrapped with %w so
// callers can errors.Is/As through the whole chain (REQ: %w wrapping).
func doGet(ctx context.Context, rawURL string, customize func(*http.Request)) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("autocomplete: build request for %s: %w", rawURL, err)
	}
	if customize != nil {
		customize(req)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("autocomplete: GET %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("autocomplete: read response body from %s: %w", rawURL, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("autocomplete: GET %s returned status %s", rawURL, resp.Status)
	}
	return body, nil
}

// Default base URLs, matching the upstream autocomplete.py exactly. Each
// backend constructor takes a baseURL so tests can inject an httptest.Server
// (the test seam); the production registry binds these defaults.
const (
	defaultDuckDuckGoBaseURL = "https://duckduckgo.com/ac"
	defaultGoogleBaseURL     = "https://www.google.com/complete/search"
	defaultWikipediaBaseURL  = "https://en.wikipedia.org/w/api.php"
	defaultBingBaseURL       = "https://www.bing.com/AS/Suggestions"
	defaultBraveBaseURL      = "https://search.brave.com/api/suggest"
)

// backends is the registry of autocomplete providers. The keys are the
// backend names of REQ-019 (also the values accepted by the
// search.autocomplete setting; an empty setting means "duckduckgo").
var backends = map[string]Backend{
	"duckduckgo":      duckduckgoBackend(defaultDuckDuckGoBaseURL),
	"google_complete": googleCompleteBackend(defaultGoogleBaseURL),
	"wikipedia":       wikipediaBackend(defaultWikipediaBaseURL),
	"bing":            bingBackend(defaultBingBaseURL),
	"brave":           braveBackend(defaultBraveBaseURL),
}

// Search runs the named backend and returns its suggestions. An unknown
// backend name yields an error wrapping ErrUnknownBackend; backend network
// or parse failures are returned wrapped as well.
func Search(backendName string, ctx context.Context, query string, locale string) ([]string, error) {
	backend, ok := backends[backendName]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownBackend, backendName)
	}
	return backend(ctx, query, locale)
}
