package fetch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testHTML = `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><title>Test</title></head><body><p>Héllo wörld</p></body></html>`

func TestGetHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, testHTML)
	}))
	defer srv.Close()

	c, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := c.Get(context.Background(), srv.URL+"/page")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if resp.ContentType != "text/html; charset=utf-8" {
		t.Errorf("ContentType = %q, want %q", resp.ContentType, "text/html; charset=utf-8")
	}
	if resp.Charset != "utf-8" {
		t.Errorf("Charset = %q, want utf-8", resp.Charset)
	}
	if string(resp.Body) != testHTML {
		t.Errorf("Body mismatch:\n got %q\nwant %q", resp.Body, testHTML)
	}
	if resp.Truncated {
		t.Error("Truncated = true, want false")
	}
	wantURL := srv.URL + "/page"
	if resp.URL != wantURL || resp.FinalURL != wantURL {
		t.Errorf("URL = %q, FinalURL = %q, want %q", resp.URL, resp.FinalURL, wantURL)
	}
}

// redirectServer serves GET /c/<max>/<n>: redirects to /c/<max>/<n+1> until
// n >= max, then returns 200. A request to /c/6/1 therefore follows exactly
// MaxRedirects=5 redirects before landing on /c/6/6; /c/7/1 triggers a 6th
// redirect and must be rejected.
func redirectServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var max, n int
		if _, err := fmt.Sscanf(r.URL.Path, "/c/%d/%d", &max, &n); err != nil {
			http.NotFound(w, r)
			return
		}
		if n >= max {
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, testHTML)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/c/%d/%d", max, n+1), http.StatusFound)
	}))
}

func TestRedirectLimit(t *testing.T) {
	srv := redirectServer(t)
	defer srv.Close()

	c, err := New(Config{MaxRedirects: 5})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Exactly MaxRedirects redirects: followed successfully.
	resp, err := c.Get(context.Background(), srv.URL+"/c/6/1")
	if err != nil {
		t.Fatalf("Get with 5 redirects: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if resp.FinalURL != srv.URL+"/c/6/6" {
		t.Errorf("FinalURL = %q, want %q", resp.FinalURL, srv.URL+"/c/6/6")
	}

	// One redirect over the limit: rejected with ErrTooManyRedirects.
	_, err = c.Get(context.Background(), srv.URL+"/c/7/1")
	if err == nil {
		t.Fatal("Get with 6 redirects: expected error, got nil")
	}
	if !errors.Is(err, ErrTooManyRedirects) {
		t.Errorf("error = %v, want ErrTooManyRedirects", err)
	}
}

func TestRedirectUnsupportedScheme(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/file" {
			w.Header().Set("Location", "file:///etc/passwd")
		} else {
			w.Header().Set("Location", "javascript:alert(1)")
		}
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	c, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, p := range []string{"/file", "/js"} {
		_, err := c.Get(context.Background(), srv.URL+p)
		if err == nil {
			t.Fatalf("Get(%s): expected error, got nil", p)
		}
		if !errors.Is(err, ErrUnsupportedScheme) {
			t.Errorf("Get(%s): error = %v, want ErrUnsupportedScheme", p, err)
		}
	}
}

func TestNotHTML(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
	}{
		{"text/plain", "text/plain", "just text"},
		{"application/json", "application/json", `{"a":1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
				_, _ = fmt.Fprint(w, tt.body)
			}))
			defer srv.Close()

			c, err := New(Config{})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, err = c.Get(context.Background(), srv.URL)
			if err == nil {
				t.Fatal("expected ErrNotHTML, got nil")
			}
			if !errors.Is(err, ErrNotHTML) {
				t.Fatalf("error = %v, want ErrNotHTML", err)
			}
			var nhe *NotHTMLError
			if !errors.As(err, &nhe) {
				t.Fatalf("error %v is not *NotHTMLError", err)
			}
			if nhe.ContentType != tt.contentType {
				t.Errorf("ContentType = %q, want %q", nhe.ContentType, tt.contentType)
			}
		})
	}
}

func TestMissingContentType(t *testing.T) {
	// Body looks like HTML: accepted (pragmatic override, documented in
	// client.go). w.Header()["Content-Type"] = nil suppresses Go's
	// automatic content sniffing so no Content-Type is sent.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header()["Content-Type"] = nil
		_, _ = fmt.Fprint(w, testHTML)
	}))
	defer srv.Close()

	c, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if resp.ContentType != "" {
		t.Errorf("ContentType = %q, want empty", resp.ContentType)
	}
	if string(resp.Body) != testHTML {
		t.Error("Body mismatch")
	}
}

func TestMissingContentTypeNotHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header()["Content-Type"] = nil
		_, _ = fmt.Fprint(w, "this is not html, just text")
	}))
	defer srv.Close()

	c, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Get(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error for non-HTML body without Content-Type, got nil")
	}
	if !errors.Is(err, ErrNotHTML) {
		t.Errorf("error = %v, want ErrNotHTML", err)
	}
}

func TestTruncated(t *testing.T) {
	body := strings.Repeat("a", 200)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, body)
	}))
	defer srv.Close()

	c, err := New(Config{MaxBytes: 64})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !resp.Truncated {
		t.Error("Truncated = false, want true")
	}
	if len(resp.Body) != 64 {
		t.Errorf("len(Body) = %d, want 64", len(resp.Body))
	}
	if string(resp.Body) != body[:64] {
		t.Error("Body does not match first 64 bytes")
	}
}

func TestInvalidURL(t *testing.T) {
	c, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tests := []struct {
		name string
		url  string
		want error // nil → any descriptive error is fine
	}{
		{"empty scheme", ":", nil},
		{"ftp", "ftp://example.com/index.html", ErrUnsupportedScheme},
		{"file", "file:///etc/passwd", ErrUnsupportedScheme},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := c.Get(context.Background(), tt.url)
			if err == nil {
				t.Fatalf("Get(%q): expected error, got nil", tt.url)
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Errorf("Get(%q): error = %v, want %v", tt.url, err, tt.want)
			}
		})
	}
}

func TestTimeout(t *testing.T) {
	released := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-released // block until the test releases us
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, testHTML)
	}))
	defer func() {
		close(released)
		srv.Close()
	}()

	c, err := New(Config{TimeoutSec: 1})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Get(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want context.DeadlineExceeded", err)
	}
}

func TestHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Get(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected HTTPError, got nil")
	}
	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("error = %v, want *HTTPError", err)
	}
	if he.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want 500", he.StatusCode)
	}
	if he.Status != "500 Internal Server Error" {
		t.Errorf("Status = %q, want %q", he.Status, "500 Internal Server Error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error message %q does not mention the status", err.Error())
	}
}

func TestUserAgent(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.UserAgent()
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, testHTML)
	}))
	defer srv.Close()

	// Custom UA is honored (NFR-003: configurable and identifiable).
	custom := "nanuq-test/1.0"
	c, err := New(Config{UserAgent: custom})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Get(context.Background(), srv.URL); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != custom {
		t.Errorf("User-Agent = %q, want %q", got, custom)
	}

	// Default UA is applied when none is configured.
	c2, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c2.Get(context.Background(), srv.URL); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != defaultUserAgent {
		t.Errorf("User-Agent = %q, want default %q", got, defaultUserAgent)
	}
}

func TestNewDefaults(t *testing.T) {
	c, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.http.Timeout != defaultTimeoutSec*time.Second {
		t.Errorf("Timeout = %v, want %ds", c.http.Timeout, defaultTimeoutSec)
	}
	if c.maxBytes != defaultMaxBytes {
		t.Errorf("maxBytes = %d, want %d", c.maxBytes, defaultMaxBytes)
	}
	if c.maxRedirects != defaultMaxRedirects {
		t.Errorf("maxRedirects = %d, want %d", c.maxRedirects, defaultMaxRedirects)
	}
	if c.userAgent != defaultUserAgent {
		t.Errorf("userAgent = %q, want %q", c.userAgent, defaultUserAgent)
	}
}

func TestNewValidation(t *testing.T) {
	for _, cfg := range []Config{
		{TimeoutSec: -1},
		{MaxBytes: -1},
		{MaxRedirects: -1},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("New(%+v): expected error, got nil", cfg)
		}
	}
}

func TestCharsetFromMeta(t *testing.T) {
	// NOTE: WHATWG maps "iso-8859-1" to windows-1252 (web-compatible alias),
	// so iso-8859-15 is used here to prove <meta> prescan detection.
	body := "<html><head><meta charset=\"iso-8859-15\"><title>x</title></head><body>caf\xe9</body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, body)
	}))
	defer srv.Close()

	c, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if resp.Charset != "iso-8859-15" {
		t.Errorf("Charset = %q, want iso-8859-15", resp.Charset)
	}
}
