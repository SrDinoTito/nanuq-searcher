// limiter.go — rate limiter por IP (TASK-016, REQ-016).
//
// Port de example/searxng/searx/limiter.py + botdetection/ip_limit.py
// (DECISION-010): se porta ÚNICAMENTE el rate-limit IP esencial por ventanas
// deslizantes. La detección heurística de bots (http_accept, http_sec_fetch,
// link_token, etc.) NO se porta.
//
// Orden del filtro (fiel a limiter.filter_request): PASSLIST → BLOCKLIST →
// checks. PASSLIST/BLOCKLIST son hooks no-op: las listas de limiter.toml no
// se portan en esta fase, pero el orden se preserva.
//
// Las claves de valkey son el HMAC-SHA256 de la IP (cache.Secret.Hash), de
// modo que "solo el valor hash de una IP se almacena en la base valkey"
// (docstring de ip_limit.py).
package limiter

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	goredis "github.com/redis/go-redis/v9"

	"nanuq-engine/internal/cache"
)

// ErrRateLimited es el sentinel que FilterRequest devuelve cuando una IP
// supera un límite de ventana. El middleware HTTP lo traduce a 429.
var ErrRateLimited = errors.New("limiter: too many requests")

// Filter abstrae el rate limiter para el middleware HTTP (handlers.Limiter-
// Middleware) y permite inyectar fakes en los tests del paquete webapp.
type Filter interface {
	// FilterRequest aplica las políticas de rate limiting al request r.
	// Devuelve nil si la request procede, ErrRateLimited (o error envuelto
	// que lo contiene) si supera un límite, y cualquier otro error cuando
	// valkey no responde (el middleware lo trata como 429 fail-closed).
	FilterRequest(ctx context.Context, r *http.Request) error
}

// Config reúne los parámetros de construcción del Limiter. Los campos se
// obtienen de internal/config en el wiring (TASK-022); aquí solo se consumen.
type Config struct {
	// Enabled activa el rate limiting. false ⇒ New devuelve (nil, nil) y el
	// middleware es un no-op (cfg.Server.Limiter).
	Enabled bool
	// PublicInstance es cfg.Server.PublicInstance; se conserva para que el
	// wiring valide valkey con cache.RequireValkey (EC-010).
	PublicInstance bool
	// ValkeyURL es cfg.Valkey.URL (string) — el cliente valkey donde viven
	// las ventanas deslizantes.
	ValkeyURL string
	// Secret es cfg.Server.SecretKey ya parseado como cache.Secret; se usa
	// para hashear las IPs antes de tocar valkey.
	Secret cache.Secret
}

// Limiter implementa Filter sobre valkey. Es seguro para uso concurrente:
// el cliente go-redis es thread-safe y Limiter es inmutable tras New.
type Limiter struct {
	client *goredis.Client
	secret cache.Secret
}

// New construye un Limiter. Con cfg.Enabled=false devuelve (nil, nil): el
// caller debe propagar el nil (sin limiter = middleware no-op, ver
// server.WithLimiter). Errores: secret vacía (EC-010, misma condición que
// cache.NewSecret) y valkey no configurado/alcanzable (cache.NewFromURL).
func New(cfg Config) (*Limiter, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if strings.TrimSpace(string(cfg.Secret)) == "" {
		return nil, fmt.Errorf("limiter: secret key vacía — limiter requiere cfg.Server.SecretKey (EC-010)")
	}
	client, err := cache.NewFromURL(cfg.ValkeyURL)
	if err != nil {
		return nil, fmt.Errorf("limiter: %w", err)
	}
	return &Limiter{client: client, secret: cfg.Secret}, nil
}

// Close libera el cliente valkey. Es idempotente (client nil ⇒ no-op), lo que
// permite llamarlo incluso cuando New devolvió un Limiter parcial.
func (l *Limiter) Close() error {
	if l == nil || l.client == nil {
		return nil
	}
	return l.client.Close()
}

// FilterRequest aplica las políticas de rate limiting de ip_limit.py al
// request. Orden: PASSLIST → BLOCKLIST (hooks no-op) → API → BURST → LONG.
//
// Desviación documentada: upstream también chequea link-local antes de las
// listas y aplica las ventanas SUSPICIOUS cuando hay link_token; aquí no hay
// detección de bots (DECISION-010), por lo que SUSPICIOUS_* no se aplica
// (ver sliding.go).
func (l *Limiter) FilterRequest(ctx context.Context, r *http.Request) error {
	// PASSLIST: ip_lists.pass_ip — si la IP estuviera en la lista blanca,
	// filter_request devolvería None aquí. Listas no portadas (limiter.toml
	// no existe en nanuq): hook no-op preservando el orden del upstream.
	// BLOCKLIST: ip_lists.block_ip — si la IP estuviera en la lista negra,
	// filter_request devolvería 429 aquí. Hook no-op por el mismo motivo.

	ip := getIP(r)
	if ip == "" {
		return fmt.Errorf("limiter: request sin IP remota (RemoteAddr=%q)", r.RemoteAddr)
	}
	// "only the hash value of an IP is stored in the valkey DB".
	ipHash := l.secret.Hash(ip)

	// API window: requests con format != html (JSON/CSV/RSS…) se tratan como
	// acceso API — 4 por IP y hora. Upstream chequea request.args.get(
	// 'format', 'html') sobre todas las rutas; aquí se acota a /search, la
	// única ruta que consume format (EC-008).
	if r.URL.Path == "/search" && r.FormValue("format") != "html" {
		if err := checkWindow(ctx, l.client, windowKey("API_WINDOW", ipHash), API_WINDOW, API_MAX); err != nil {
			return err
		}
	}

	// BURST: ráfaga — 15 requests por IP en 20s.
	if err := checkWindow(ctx, l.client, windowKey("BURST_WINDOW", ipHash), BURST_WINDOW, BURST_MAX); err != nil {
		return err
	}
	// LONG: ventana larga — 150 requests por IP en 10min.
	if err := checkWindow(ctx, l.client, windowKey("LONG_WINDOW", ipHash), LONG_WINDOW, LONG_MAX); err != nil {
		return err
	}
	return nil
}

// getIP extrae la IP del request.
//
// DECISIÓN (desviación documentada, TASK-016): esta fase usa r.RemoteAddr
// (net.SplitHostPort → host) y NO respeta X-Forwarded-For. Fiel al upstream,
// que confía en el header solo tras ProxyFix con trusted proxies conocidos;
// sin esa infraestructura, confiar en XFF permitiría un bypass trivial
// (el atacante forja el header). TODO(fase trusted proxies): respetar XFF
// cuando cfg.Server.TrustedProxies esté definido, previa validación del peer
// directo (DSG-TRUSTED-PROXIES).
//
// net.SplitHostPort falla solo si RemoteAddr no tiene puerto (p.ej. algunos
// proxies); en ese caso se devuelve RemoteAddr tal cual.
func getIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// windowKey construye la clave valkey de una ventana: "ip_limit.<WINDOW>:<hash>",
// fiel al prefijo 'ip_limit.' + window de ip_limit.py pero con la IP ya
// hasheada en lugar de legible (privacy).
func windowKey(window, ipHash string) string {
	return "ip_limit." + window + ":" + ipHash
}
