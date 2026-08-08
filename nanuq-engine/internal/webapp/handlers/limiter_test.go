// limiter_test.go — tests del middleware de rate limiting (TASK-016).
//
// Package handlers_test externo (mismo patrón que misc_test.go) para poder
// importar nanuq-engine/internal/webapp sin crear el ciclo webapp -> handlers.
// El limiter real necesita valkey; aquí se inyecta un fake (limiterFake) que
// implementa limiter.Filter y falla tras N llamadas, verificando el contrato
// 429 del middleware sin infraestructura.
package handlers_test

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/webapp"
)

// errFakeRateLimited simula el sentinel limiter.ErrRateLimited del limiter
// real (el fake no importa internal/limiter para no acoplarse; solo importa
// que el middleware traduzca cualquier error a 429).
var errFakeRateLimited = errors.New("limiter: too many requests")

// limiterFake implementa limiter.Filter: permite las primeras limit llamadas
// y luego devuelve un error (equivale a ErrRateLimited del limiter real).
type limiterFake struct {
	limit int
	calls atomic.Int64
}

func (f *limiterFake) FilterRequest(ctx context.Context, r *http.Request) error {
	if f.calls.Add(1) > int64(f.limit) {
		return errFakeRateLimited
	}
	return nil
}

// newLimiterTestServer construye un Server con cfg.Server.Limiter configurado
// e inyecta el fake vía WithLimiter.
func newLimiterTestServer(t *testing.T, limiter bool, f *limiterFake) *webapp.Server {
	t.Helper()
	cfg := &config.Config{
		General: config.General{InstanceName: testInstance},
		Server:  config.Server{Limiter: limiter},
	}
	return webapp.New(cfg, nil, nil, nil).WithLimiter(f)
}

// TestLimiterMiddleware429: con el limiter habilitado, las primeras 2
// requests pasan y la 3ª recibe 429 Too Many Requests (CA-009).
func TestLimiterMiddleware429(t *testing.T) {
	srv := newLimiterTestServer(t, true, &limiterFake{limit: 2})

	for i := 0; i < 2; i++ {
		if rec := doGet(t, srv, "/autocompleter"); rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want %d", i+1, rec.Code, http.StatusOK)
		}
	}
	rec := doGet(t, srv, "/autocompleter")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("3ª request: status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
}

// TestLimiterMiddlewareDisabled: cfg.Server.Limiter=false ⇒ WithLimiter es un
// no-op y el middleware no filtra: incluso un fake que siempre falla no recibe
// llamadas (el fake nunca llega a errores).
func TestLimiterMiddlewareDisabled(t *testing.T) {
	srv := newLimiterTestServer(t, false, &limiterFake{limit: 0})

	rec := doGet(t, srv, "/autocompleter")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (limiter deshabilitado = no-op)", rec.Code, http.StatusOK)
	}
}

// TestLimiterMiddlewareUnprotected: las rutas fuera de protectedPaths no se
// filtran aunque el limiter esté habilitado (el fake nunca recibe llamadas).
func TestLimiterMiddlewareUnprotected(t *testing.T) {
	srv := newLimiterTestServer(t, true, &limiterFake{limit: 0})

	if rec := doGet(t, srv, "/healthz"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (rutas no protegidas no se filtran)", rec.Code, http.StatusOK)
	}
}
