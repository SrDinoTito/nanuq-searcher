package network

// Tests for the outbound HTTP layer (TASK-007, DSG-006). Coverage:
// request building (GET/JSON/form bodies), timeout classification,
// transport caching and the error mapping that feeds the suspension
// mechanism (REQ-008).

import (
	"context"
	"errors"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
	"nanuq-engine/internal/search"
)

// Compile-time assertion: Client implements the search.Requester seam the
// processor uses (processor.go L29-35).
var _ search.Requester = (*Client)(nil)

// --- TestDoGET (TASK-007, REQ-012): plain GET with headers and cookies ---
func TestDoGET(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		if got := r.Header.Get("X-Test"); got != "1" {
			t.Errorf("X-Test header = %q, want %q", got, "1")
		}
		if ck, err := r.Cookie("session"); err != nil || ck.Value != "abc" {
			t.Errorf("session cookie = %v, want value abc", ck)
		}
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "ok")
	}))
	defer srv.Close()

	c, err := New(&config.Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := c.Do(context.Background(), &engine.RequestParams{
		Method:  http.MethodGet,
		URL:     srv.URL,
		Headers: http.Header{"X-Test": {"1"}},
		Cookies: []*http.Cookie{{Name: "session", Value: "abc"}},
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("body = %q, want %q", body, "ok")
	}
}

// --- TestDoJSON (TASK-007): JSON body + content type ---
func TestDoJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"q":"hello"}` {
			t.Errorf("body = %q, want %q", body, `{"q":"hello"}`)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, _ := New(&config.Config{})
	resp, err := c.Do(context.Background(), &engine.RequestParams{
		Method: http.MethodPost,
		URL:    srv.URL,
		JSON:   map[string]string{"q": "hello"},
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// --- TestDoData (TASK-007): form-encoded body + content type ---
func TestDoData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q, want application/x-www-form-urlencoded", got)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "q=hello&x=1" {
			t.Errorf("body = %q, want %q", body, "q=hello&x=1")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, _ := New(&config.Config{})
	resp, err := c.Do(context.Background(), &engine.RequestParams{
		Method: http.MethodPost,
		URL:    srv.URL,
		Data:   map[string]string{"q": "hello", "x": "1"},
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// --- TestDoTimeout (TASK-007, REQ-008): slow server, per-request timeout
// must yield a suspension error with Reason "timeout" ---
func TestDoTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(250 * time.Millisecond)
	}))
	defer srv.Close()

	c, _ := New(&config.Config{})
	_, err := c.Do(context.Background(), &engine.RequestParams{
		URL:     srv.URL,
		Timeout: 50 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("Do: expected timeout error, got nil")
	}
	if !IsSuspendError(err) {
		t.Fatalf("Do: error %v is not a suspension error", err)
	}
	var susErr *engine.EngineSuspendError
	if !errors.As(err, &susErr) {
		t.Fatalf("Do: %v does not unwrap to EngineSuspendError", err)
	}
	if susErr.Reason != "timeout" {
		t.Errorf("Reason = %q, want %q", susErr.Reason, "timeout")
	}
	if !errors.Is(err, ErrTimeout) {
		t.Errorf("errors.Is(err, ErrTimeout) = false, want true")
	}
}

// --- TestTransportCache (DSG-006): same config reuses the transport,
// different config builds a different one ---
func TestTransportCache(t *testing.T) {
	cfgA := &config.Config{Outgoing: config.Outgoing{EnableHTTP2: true}}
	cfgB := &config.Config{Outgoing: config.Outgoing{EnableHTTP2: false}}

	c1, err := New(cfgA)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c2, err := New(cfgB)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t1a := c1.getClient().Transport
	t1b := c1.getClient().Transport
	if t1a != t1b {
		t.Errorf("same config: transport pointers differ (%p vs %p), want shared", t1a, t1b)
	}
	t2 := c2.getClient().Transport
	if t1a == t2 {
		t.Errorf("different config: transport pointers equal (%p), want distinct", t1a)
	}
}

// --- TestRaiseForHTTPError (REQ-008): 429/403 map to suspension reasons,
// 2xx maps to nil ---
func TestRaiseForHTTPError(t *testing.T) {
	// 429 → too many requests.
	err429 := RaiseForHTTPError(&http.Response{StatusCode: http.StatusTooManyRequests}, []byte("rate limited"))
	if err429 == nil {
		t.Fatal("429: expected error, got nil")
	}
	if !IsSuspendError(err429) {
		t.Fatalf("429: %v is not a suspension error", err429)
	}
	var susErr *engine.EngineSuspendError
	if !errors.As(err429, &susErr) {
		t.Fatalf("429: %v does not unwrap to EngineSuspendError", err429)
	}
	if susErr.Reason != "too many requests" {
		t.Errorf("429: Reason = %q, want %q", susErr.Reason, "too many requests")
	}
	if !errors.Is(err429, ErrTooManyRequests) {
		t.Errorf("429: errors.Is(ErrTooManyRequests) = false, want true")
	}

	// 403 → access denied.
	err403 := RaiseForHTTPError(&http.Response{StatusCode: http.StatusForbidden}, []byte("forbidden"))
	if err403 == nil {
		t.Fatal("403: expected error, got nil")
	}
	if !errors.As(err403, &susErr) || susErr.Reason != "access denied" {
		t.Errorf("403: Reason = %v, want %q", susErr.Reason, "access denied")
	}
	if !errors.Is(err403, ErrAccessDenied) {
		t.Errorf("403: errors.Is(ErrAccessDenied) = false, want true")
	}

	// Cloudflare 403 page → cf_browser (looked up in suspended_times).
	errCf := RaiseForHTTPError(&http.Response{StatusCode: http.StatusForbidden}, []byte(`<html>cf-browser-verification</html>`))
	if errCf == nil {
		t.Fatal("cloudflare: expected error, got nil")
	}
	if !errors.As(errCf, &susErr) || susErr.Reason != "cf_browser" {
		t.Errorf("cloudflare: Reason = %v, want %q", susErr.Reason, "cf_browser")
	}

	// 200 → nil.
	if err := RaiseForHTTPError(&http.Response{StatusCode: http.StatusOK}, []byte("ok")); err != nil {
		t.Errorf("200: got %v, want nil", err)
	}

	// nil resp → nil.
	if err := RaiseForHTTPError(nil, nil); err != nil {
		t.Errorf("nil resp: got %v, want nil", err)
	}
}

// --- TestShuffleCiphers (CON-003): first three suites fixed, remaining
// permuted, length and multiset preserved ---
func TestShuffleCiphers(t *testing.T) {
	original := []uint16{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13}

	shuffled := make([]uint16, len(original))
	copy(shuffled, original)
	ShuffleCiphers(shuffled)

	// Length preserved.
	if len(shuffled) != len(original) {
		t.Fatalf("len = %d, want %d", len(shuffled), len(original))
	}
	// First three positions stay fixed.
	for i := 0; i < 3; i++ {
		if shuffled[i] != original[i] {
			t.Errorf("position %d = %d, want %d (first three must stay fixed)", i, shuffled[i], original[i])
		}
	}
	// The tail must be a permutation of the original tail (same multiset,
	// no loss/duplication).
	tailOrig := append([]uint16(nil), original[3:]...)
	tailShuf := append([]uint16(nil), shuffled[3:]...)
	sort.Slice(tailOrig, func(i, j int) bool { return tailOrig[i] < tailOrig[j] })
	sort.Slice(tailShuf, func(i, j int) bool { return tailShuf[i] < tailShuf[j] })
	if len(tailOrig) != len(tailShuf) {
		t.Fatalf("tail len = %d, want %d", len(tailShuf), len(tailOrig))
	}
	for i := range tailOrig {
		if tailOrig[i] != tailShuf[i] {
			t.Fatalf("tail multiset mismatch at %d: %d vs %d", i, tailOrig[i], tailShuf[i])
		}
	}
	// The tail must actually be permuted (not the identity order). Repeat
	// with several seeds; the probability of every shuffle returning the
	// identity is ~(1/10!)^n ≈ 0.
	permuted := false
	for seed := int64(0); seed < 50 && !permuted; seed++ {
		rand.Seed(seed)
		probe := make([]uint16, len(original))
		copy(probe, original)
		ShuffleCiphers(probe)
		permuted = !equalSlices(probe[3:], original[3:])
	}
	if !permuted {
		t.Error("shuffle produced the identity order 50 times in a row")
	}

	// Slices with three or fewer elements are untouched.
	short := []uint16{7, 8, 9}
	before := append([]uint16(nil), short...)
	ShuffleCiphers(short)
	if !equalSlices(short, before) {
		t.Errorf("short slice mutated: %v -> %v", before, short)
	}
}

func equalSlices(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
