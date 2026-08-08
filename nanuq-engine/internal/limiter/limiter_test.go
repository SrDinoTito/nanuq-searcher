// limiter_test.go — tests del rate limiter IP (TASK-016).
//
// Siguen el patrón de internal/cache/cache_test.go: los tests que necesitan
// valkey vivo se saltan (t.Skip) cuando REDIS_URL no está definido, y usan
// claves únicas por ejecución para no contaminarse entre runs.
package limiter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"nanuq-engine/internal/cache"
)

// ipCounter genera IPs de prueba únicas por ejecución (bloque 192.0.2.0/24 de
// documentación, TEST-NET-1) para que las ventanas valkey no colisionen entre
// tests ni entre runs.
var ipCounter atomic.Int64

func testIP(t *testing.T) string {
	t.Helper()
	n := ipCounter.Add(1)
	return fmt.Sprintf("192.0.2.%d", n%250+1)
}

// newTestLimiter construye un Limiter con valkey vivo; SKIP si no hay
// REDIS_URL, como cache_test.go.
func newTestLimiter(t *testing.T) *Limiter {
	t.Helper()
	url := os.Getenv("REDIS_URL")
	if url == "" {
		t.Skip("REDIS_URL no definida: se omiten los tests de ventanas valkey (patrón cache_test.go)")
	}
	secret, err := cache.NewSecret("limiter-test-secret")
	if err != nil {
		t.Fatalf("NewSecret: %v", err)
	}
	l, err := New(Config{Enabled: true, ValkeyURL: url, Secret: secret})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

// req construye un request GET a path con la IP de prueba como RemoteAddr.
func req(ip, path string) *http.Request {
	r := httptest.NewRequest("GET", path, nil)
	r.RemoteAddr = ip + ":12345"
	return r
}

// TestLimiterBurst: BURST 20s/15 — 15 requests OK, la 16ª → ErrRateLimited
// (CA-009). IP única por test para aislar la ventana.
func TestLimiterBurst(t *testing.T) {
	l := newTestLimiter(t)
	ctx := context.Background()
	ip := testIP(t)

	for i := 0; i < 15; i++ {
		if err := l.FilterRequest(ctx, req(ip, "/search")); err != nil {
			t.Fatalf("request %d: inesperado %v", i+1, err)
		}
	}
	if err := l.FilterRequest(ctx, req(ip, "/search")); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("16ª request: se esperaba ErrRateLimited, got %v", err)
	}
}

// TestLimiterAPIWindow: API 3600s/4 — 4 requests format=json OK, la 5ª →
// ErrRateLimited. Una request html de la misma IP no cuenta para la ventana
// API (solo cuenta para BURST/LONG, no excedidos aún).
func TestLimiterAPIWindow(t *testing.T) {
	l := newTestLimiter(t)
	ctx := context.Background()
	ip := testIP(t)

	for i := 0; i < 4; i++ {
		if err := l.FilterRequest(ctx, req(ip, "/search?format=json")); err != nil {
			t.Fatalf("request API %d: inesperado %v", i+1, err)
		}
	}
	if err := l.FilterRequest(ctx, req(ip, "/search?format=json")); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("5ª request API: se esperaba ErrRateLimited, got %v", err)
	}
	if err := l.FilterRequest(ctx, req(ip, "/search")); err != nil {
		t.Fatalf("request html tras exceder API: inesperado %v", err)
	}
}

// TestLimiterPolicyConstants: valores exactos portados de ip_limit.py.
func TestLimiterPolicyConstants(t *testing.T) {
	if BURST_WINDOW != 20 || BURST_MAX != 15 {
		t.Errorf("BURST: got %d/%d, want 20/15", BURST_WINDOW, BURST_MAX)
	}
	if LONG_WINDOW != 600 || LONG_MAX != 150 {
		t.Errorf("LONG: got %d/%d, want 600/150", LONG_WINDOW, LONG_MAX)
	}
	if API_WINDOW != 3600 || API_MAX != 4 {
		t.Errorf("API: got %d/%d, want 3600/4", API_WINDOW, API_MAX)
	}
	if SUSPICIOUS_IP_WINDOW != 3600*24*30 || SUSPICIOUS_IP_MAX != 3 {
		t.Errorf("SUSPICIOUS: got %d/%d, want 30d/3", SUSPICIOUS_IP_WINDOW, SUSPICIOUS_IP_MAX)
	}
}

// TestGetIP: net.SplitHostPort sobre RemoteAddr, con fallback cuando no hay
// puerto.
func TestGetIP(t *testing.T) {
	tests := []struct {
		remoteAddr string
		want       string
	}{
		{"192.0.2.7:1234", "192.0.2.7"},       // IPv4 con puerto
		{"[2001:db8::1]:8080", "2001:db8::1"}, // IPv6 con puerto
		{"192.0.2.9", "192.0.2.9"},            // sin puerto → fallback tal cual
		{"", ""},                              // vacío → fallback vacío
	}
	for _, tt := range tests {
		r := httptest.NewRequest("GET", "/search", nil)
		r.RemoteAddr = tt.remoteAddr
		if got := getIP(r); got != tt.want {
			t.Errorf("getIP(%q) = %q, want %q", tt.remoteAddr, got, tt.want)
		}
	}
}

// TestWindowKey: prefijo fiel a ip_limit.py + hash HMAC de la IP.
func TestWindowKey(t *testing.T) {
	secret, err := cache.NewSecret("k")
	if err != nil {
		t.Fatal(err)
	}
	ipHash := secret.Hash("192.0.2.1")
	if got := windowKey("BURST_WINDOW", ipHash); got != "ip_limit.BURST_WINDOW:"+ipHash {
		t.Errorf("windowKey = %q", got)
	}
}

// TestNewDisabled: Enabled=false → (nil, nil) sin tocar valkey.
func TestNewDisabled(t *testing.T) {
	l, err := New(Config{Enabled: false})
	if err != nil || l != nil {
		t.Errorf("New(disabled) = (%v, %v), want (nil, nil)", l, err)
	}
}

// TestNewNoSecret: Enabled=true con Secret vacía → error EC-010.
func TestNewNoSecret(t *testing.T) {
	if _, err := New(Config{Enabled: true, ValkeyURL: "redis://127.0.0.1:6379/15"}); err == nil {
		t.Error("New con Secret vacía: se esperaba error (EC-010)")
	}
}
