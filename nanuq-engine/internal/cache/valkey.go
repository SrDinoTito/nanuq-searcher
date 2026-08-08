// valkey.go — cliente Valkey (compatible Redis) con scripts Lua verbatim.
//
// TASK-015 / REQ-015 / DSG-012: port de valkeylib.py con go-redis/v9.
// Los tres scripts Lua (INCR_COUNTER, INCR_SLIDING_WINDOW, PURGE_BY_PREFIX)
// se copian TAL CUAL del .py de SearXNG: son strings Lua portables, no
// dependen del cliente Python — se ejecutan con client.Eval (go-redis).
//
// EC-010: una instancia pública SIN valkey configurado debe fallar en el
// arranque (NewFromURL("") devuelve error claro; RequireValkey valida la
// combinación public_instance + url).
package cache

import (
	"context"
	"fmt"
	"strings"

	goredis "github.com/redis/go-redis/v9"
)

// Lua scripts portados VERBATIM de searx/valkeylib.py (valkeylib.py L44-108,
// L169-179). Los scripts usan KEYS[1]/ARGV[n] y redis.call — portables a
// go-redis Eval sin adaptación.

// luaIncrCounter: contador con límite y expiración opcionales.
// ARGV[1]=limit (0 = sin límite), ARGV[2]=expire (0 = sin expiración),
// KEYS[1]=nombre del contador. Devuelve el valor del contador.
const luaIncrCounter = `
local limit = tonumber(ARGV[1])
local expire = tonumber(ARGV[2])
local c_name = KEYS[1]

local c = redis.call('GET', c_name)

if not c then
    c = redis.call('INCR', c_name)
    if expire > 0 then
        redis.call('EXPIRE', c_name, expire)
    end
else
    c = tonumber(c)
    if limit == 0 or c < limit then
       c = redis.call('INCR', c_name)
    end
end
return c
`

// luaIncrSlidingWindow: contador de ventana deslizante con sorted set.
// ARGV[1]=duration (ventana en segundos), KEYS[1]=nombre del contador.
// ZREMRANGEBYSCORE fuera de ventana + ZADD con TIME + ZCOUNT + EXPIRE.
// Devuelve el número de llamadas dentro de la ventana.
const luaIncrSlidingWindow = `
local expire = tonumber(ARGV[1])
local name = KEYS[1]
local current_time = redis.call('TIME')

redis.call('ZREMRANGEBYSCORE', name, 0, current_time[1] - expire)
redis.call('ZADD', name, current_time[1], current_time[1] .. current_time[2])
local result = redis.call('ZCOUNT', name, 0, current_time[1] + 1)
redis.call('EXPIRE', name, expire)
return result
`

// luaPurgeByPrefix: expira (no borra con DEL) todas las claves con prefijo.
// ARGV[1]=prefix. El script original usa EXPIRE en lugar de DEL: con muchas
// claves grandes, DEL bloquea el command loop; EXPIRE devuelve al instante.
// NOTA DE DESVIACIÓN: el TASK-015 menciona "SCAN + DEL" pero el script
// VERBATIM de valkeylib.py usa KEYS + EXPIRE — se conserva el texto exacto
// (DSG-012: "se copian tal cual").
const luaPurgeByPrefix = `
local prefix = tostring(ARGV[1])
for i, name in ipairs(redis.call('KEYS', prefix .. '*')) do
    redis.call('EXPIRE', name, 0)
end
`

// NewFromURL construye un cliente Valkey (go-redis) a partir de una URL
// redis://, rediss:// o unix://. Devuelve error si la URL es inválida o
// vacía (EC-010: instancia pública sin valkey → error de arranque, como el
// sys.exit de SearXNG). El caller es dueño del cliente y debe cerrarlo con
// Close().
func NewFromURL(url string) (*goredis.Client, error) {
	if strings.TrimSpace(url) == "" {
		return nil, fmt.Errorf(
			"cache: valkey: URL vacía — valkey es obligatorio cuando server.public_instance=true (EC-010)",
		)
	}
	opt, err := goredis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("cache: valkey: parse URL: %w", err)
	}
	return goredis.NewClient(opt), nil
}

// RequireValkey valida EC-010 en el arranque: si publicInstance es true pero
// la url de valkey no está configurada, devuelve un error claro (equivalente
// al sys.exit de SearXNG). El wiring lo invocará en TASK-014/022.
func RequireValkey(publicInstance bool, url string) error {
	if publicInstance && strings.TrimSpace(url) == "" {
		return fmt.Errorf(
			"cache: valkey: server.public_instance=true pero valkey.url no está configurado (EC-010)",
		)
	}
	return nil
}

// IncrCounter ejecuta INCR_COUNTER y devuelve el valor del contador.
//
// limit (0 = sin límite) frena el incremento; expire (0 = sin expiración)
// fija la vida del contador en segundos. El nombre de la clave lo decide el
// caller (el prefijo SearXNG_counter_ + secret_hash de valkeylib.py se deja
// al limiter/usuarios: ver desviación en el reporte).
func IncrCounter(ctx context.Context, client *goredis.Client, name string, limit, expire int64) (int64, error) {
	if client == nil {
		return 0, fmt.Errorf("cache: valkey: IncrCounter: cliente nil")
	}
	n, err := client.Eval(ctx, luaIncrCounter, []string{name}, limit, expire).Int64()
	if err != nil {
		return 0, fmt.Errorf("cache: valkey: INCR_COUNTER %q: %w", name, err)
	}
	return n, nil
}

// IncrSlidingWindow ejecuta INCR_SLIDING_WINDOW y devuelve el número de
// llamadas dentro de la ventana de duration segundos.
func IncrSlidingWindow(ctx context.Context, client *goredis.Client, name string, duration int64) (int64, error) {
	if client == nil {
		return 0, fmt.Errorf("cache: valkey: IncrSlidingWindow: cliente nil")
	}
	n, err := client.Eval(ctx, luaIncrSlidingWindow, []string{name}, duration).Int64()
	if err != nil {
		return 0, fmt.Errorf("cache: valkey: INCR_SLIDING_WINDOW %q: %w", name, err)
	}
	return n, nil
}

// PurgeByPrefix ejecuta PURGE_BY_PREFIX: expira todas las claves que
// empiezan por prefix (default SearXNG_ en valkeylib.py).
func PurgeByPrefix(ctx context.Context, client *goredis.Client, prefix string) error {
	if client == nil {
		return fmt.Errorf("cache: valkey: PurgeByPrefix: cliente nil")
	}
	// El script Lua solo usa ARGV[1] (sin KEYS): keys nil como en Python.
	if err := client.Eval(ctx, luaPurgeByPrefix, nil, prefix).Err(); err != nil {
		return fmt.Errorf("cache: valkey: PURGE_BY_PREFIX %q: %w", prefix, err)
	}
	return nil
}
