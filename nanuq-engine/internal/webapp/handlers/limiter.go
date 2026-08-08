// limiter.go — middleware de rate limiting por IP (TASK-016, REQ-016).
//
// Envuelve las rutas que generan carga de búsqueda (/search, /autocompleter)
// con el limiter del paquete internal/limiter. Cuando una IP supera un
// límite, se responde 429 Too Many Requests con un cuerpo de texto simple
// (CA-009: la 16ª petición de la misma IP en 20s recibe 429).
//
// El middleware es un no-op cuando el limiter es nil (cfg.Server.Limiter
// deshabilitado, ver server.WithLimiter). Los errores de valkey se tratan
// como 429 (fail-closed): si el backend de ventanas no responde, no se puede
// verificar el límite y bloquear es la opción segura para un rate limiter.
package handlers

import (
	"log/slog"
	"net/http"

	"nanuq-engine/internal/limiter"
)

// protectedPaths son las rutas que consume el rate limiter IP. /search es el
// objetivo del upstream (limiter.py solo filtra /search); /autocompleter se
// añade porque genera tráfico por keystroke y la TASK-016 lo exige.
var protectedPaths = map[string]bool{
	"/search":        true,
	"/autocompleter": true,
}

// LimiterMiddleware envuelve next con el rate limiter IP: solo las rutas de
// protectedPaths se filtran; el resto fluye directo. l == nil ⇒ no-op.
func LimiterMiddleware(l limiter.Filter, next http.Handler) http.Handler {
	if l == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !protectedPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		if err := l.FilterRequest(r.Context(), r); err != nil {
			// 429 en ambos casos: límite superado (ErrRateLimited) o valkey
			// caído (fail-closed). El mensaje es genérico a propósito: no
			// revela si valkey está operativo.
			slog.Warn("rate limited", "method", r.Method, "path", r.URL.Path, "err", err)
			http.Error(w, "429 Too Many Requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
