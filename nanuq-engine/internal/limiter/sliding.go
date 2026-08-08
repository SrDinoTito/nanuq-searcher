// sliding.go — ventanas deslizantes de rate limiting (TASK-016, REQ-016).
//
// Port de las constantes de política de example/searxng/searx/botdetection/
// ip_limit.py (DECISION-010: se porta el rate-limit IP esencial, NO la
// detección de bots ni los límites _SUSPICIOUS, que dependen de link_token).
//
// Las claves de ventana se pasan ya hasheadas (HMAC vía cache.Secret.Hash):
// "solo el valor hash de una IP se almacena en la base valkey" — docstring de
// ip_limit.py.
package limiter

import (
	"context"
	"fmt"

	goredis "github.com/redis/go-redis/v9"

	"nanuq-engine/internal/cache"
)

// Políticas de rate limiting por IP, portadas de ip_limit.py (sin variantes
// _SUSPICIOUS, que pertenecen al flujo link_token no portado).
const (
	// BURST_WINDOW / BURST_MAX: ráfaga — 15 requests por IP en 20s.
	BURST_WINDOW int64 = 20
	BURST_MAX    int64 = 15

	// LONG_WINDOW / LONG_MAX: ventana larga — 150 requests por IP en 10min.
	LONG_WINDOW int64 = 600
	LONG_MAX    int64 = 150

	// API_WINDOW / API_MAX: acceso API (format != html) — 4 requests por IP
	// en 1h.
	API_WINDOW int64 = 3600
	API_MAX    int64 = 4

	// SUSPICIOUS_IP_WINDOW / SUSPICIOUS_IP_MAX: 3 marcas de IP sospechosa en
	// 30 días. Definidas por fidelidad a ip_limit.py; NO se aplican todavía
	// porque requieren link_token (botdetection no portado, DECISION-010).
	SUSPICIOUS_IP_WINDOW int64 = 3600 * 24 * 30
	SUSPICIOUS_IP_MAX    int64 = 3
)

// checkWindow aplica una ventana deslizante sobre la clave name y devuelve
// ErrRateLimited (envuelto) cuando el número de llamadas en la ventana supera
// el límite. name ya debe estar hasheada (Secret.Hash). Los errores de valkey
// se propagan sin wrap de ErrRateLimited: el middleware decide la respuesta
// (fail-closed → 429, ver handlers.LimiterMiddleware).
func checkWindow(ctx context.Context, client *goredis.Client, name string, windowSec, limit int64) error {
	n, err := cache.IncrSlidingWindow(ctx, client, name, windowSec)
	if err != nil {
		return err
	}
	if n > limit {
		return fmt.Errorf("%w: %s: %d > %d", ErrRateLimited, name, n, limit)
	}
	return nil
}
