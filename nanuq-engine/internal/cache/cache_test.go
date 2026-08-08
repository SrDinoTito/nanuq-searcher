// cache_test.go — tests de ExpireCache (SQLite), HMAC y scripts Lua valkey.
//
// TASK-015 validación: set/get/expire/maintenance (ExpireCache), HMAC
// determinista, y valkey con skip si no hay servidor (REDIS_URL).
package cache

import (
	"context"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sample es el tipo concreto usado en el round-trip gob de structs. gob
// exige registro (gob.Register) para tipos no built-in que viajan por
// interfaces (Get devuelve any) — contrato documentado en gob.go.
type sample struct {
	Name  string
	Count int
}

func init() { gob.Register(sample{}) }

// newTestCache abre una ExpireCache sobre un archivo SQLite en t.TempDir().
// La conexión se cierra automáticamente al terminar el test.
func newTestCache(t *testing.T) *ExpireCache {
	t.Helper()
	secret, err := NewSecret("test-secret")
	if err != nil {
		t.Fatalf("NewSecret: %v", err)
	}
	c, err := Open(Config{
		Path:   filepath.Join(t.TempDir(), "cache.db"),
		Secret: secret,
		Hold:   time.Minute,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestExpireCachePutGet verifica el round-trip gob de distintos tipos y el
// contrato Get(key) (any, bool, error).
func TestExpireCachePutGet(t *testing.T) {
	c := newTestCache(t)

	// struct con gob round-trip a través del any devuelto (sample registrado
	// con gob.Register en este paquete)
	want := sample{Name: "gob", Count: 42}
	if err := c.Put("struct", want, time.Minute); err != nil {
		t.Fatalf("Put(struct): %v", err)
	}
	got, ok, err := c.Get("struct")
	if err != nil {
		t.Fatalf("Get(struct): %v", err)
	}
	if !ok {
		t.Fatal("Get(struct): ok=false, esperaba true")
	}
	gotS, isSample := got.(sample)
	if !isSample || gotS != want {
		t.Fatalf("Get(struct): got %#v (type %T), want %#v", got, got, want)
	}

	// string
	if err := c.Put("str", "hello", time.Minute); err != nil {
		t.Fatalf("Put(str): %v", err)
	}
	if v, ok, err := c.Get("str"); err != nil || !ok || v != "hello" {
		t.Fatalf("Get(str): v=%v ok=%v err=%v", v, ok, err)
	}

	// nil: valor presente con valor nil (distinto de miss)
	if err := c.Put("nil", nil, time.Minute); err != nil {
		t.Fatalf("Put(nil): %v", err)
	}
	if v, ok, err := c.Get("nil"); err != nil || !ok || v != nil {
		t.Fatalf("Get(nil): v=%v ok=%v err=%v, esperaba nil/true/nil", v, ok, err)
	}

	// clave inexistente: miss (nil, false, nil)
	if v, ok, err := c.Get("missing"); err != nil || ok || v != nil {
		t.Fatalf("Get(missing): v=%v ok=%v err=%v, esperaba nil/false/nil", v, ok, err)
	}

	// valor por encima de MaxValueLen se rechaza
	if err := c.Put("big", strings.Repeat("x", 11*1024), time.Minute); err == nil {
		t.Fatal("Put(big): esperaba error por exceder MaxValueLen")
	}
}

// TestExpireCacheTTL verifica la expiración con TTL corto.
//
// NOTA sobre la prueba: usa ttl 100ms + sleep 200ms. La columna expire_at
// es REAL con sub-segundo (desviación del INTEGER de SearXNG), así que el
// TTL de 100ms se respeta de verdad. El margen de 200ms deja holgura en CI
// lento. Alternativa futura: reloj inyectable en ExpireCache para eliminar
// el sleep (documentado como mejora).
func TestExpireCacheTTL(t *testing.T) {
	c := newTestCache(t)

	if err := c.Put("short", "value", 100*time.Millisecond); err != nil {
		t.Fatalf("Put(short): %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	if v, ok, err := c.Get("short"); err != nil || ok {
		t.Fatalf("Get(short) tras expire: v=%v ok=%v err=%v, esperaba miss", v, ok, err)
	}

	// una clave con TTL por defecto (0 => hold) sigue viva
	if err := c.Put("long", "value", 0); err != nil {
		t.Fatalf("Put(long): %v", err)
	}
	if _, ok, err := c.Get("long"); err != nil || !ok {
		t.Fatalf("Get(long): ok=%v err=%v, esperaba true", ok, err)
	}
}

// TestExpireCacheDelete verifica Delete y que borrar una clave inexistente
// no es error.
func TestExpireCacheDelete(t *testing.T) {
	c := newTestCache(t)

	if err := c.Put("k", "v", time.Minute); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := c.Delete("k"); err != nil {
		t.Fatalf("Delete(k): %v", err)
	}
	if v, ok, err := c.Get("k"); err != nil || ok || v != nil {
		t.Fatalf("Get(k) tras Delete: v=%v ok=%v err=%v, esperaba miss", v, ok, err)
	}
	if err := c.Delete("missing"); err != nil {
		t.Fatalf("Delete(missing) no debería fallar: %v", err)
	}
}

// TestExpireCacheMaintenance verifica el borrado de filas expiradas y el
// wal_checkpoint (que se ejecuta sin error).
func TestExpireCacheMaintenance(t *testing.T) {
	c := newTestCache(t)

	if err := c.Put("expired", "x", 100*time.Millisecond); err != nil {
		t.Fatalf("Put(expired): %v", err)
	}
	if err := c.Put("kept", "y", time.Minute); err != nil {
		t.Fatalf("Put(kept): %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	removed, err := c.Maintenance()
	if err != nil {
		t.Fatalf("Maintenance: %v", err)
	}
	if removed != 1 {
		t.Fatalf("Maintenance: removed=%d, esperaba 1", removed)
	}
	// la fila no expirada sobrevive
	if _, ok, err := c.Get("kept"); err != nil || !ok {
		t.Fatalf("Get(kept) tras Maintenance: ok=%v err=%v", ok, err)
	}
	// segunda ejecución sin expiradas: no borra nada
	removed, err = c.Maintenance()
	if err != nil {
		t.Fatalf("Maintenance 2: %v", err)
	}
	if removed != 0 {
		t.Fatalf("Maintenance 2: removed=%d, esperaba 0", removed)
	}
}

// TestExpireCacheSecretRotation verifica que al rotar el secret la cache se
// trunca (las claves hasheadas dejan de ser resolubles) y que con el mismo
// secret los valores persisten entre aperturas.
func TestExpireCacheSecretRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")

	open := func(t *testing.T, secretStr string) *ExpireCache {
		t.Helper()
		s, err := NewSecret(secretStr)
		if err != nil {
			t.Fatalf("NewSecret: %v", err)
		}
		c, err := Open(Config{Path: path, Secret: s})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		// t.Cleanup cierra SIEMPRE la conexión (aunque el test falle con
		// Fatalf) — evita el file-lock de Windows en la limpieza de t.TempDir.
		t.Cleanup(func() { _ = c.Close() })
		return c
	}

	// primer secret: se persiste el valor
	c1 := open(t, "one")
	if err := c1.Put("k", "v", time.Minute); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// mismo secret reabierto: el valor sobrevive
	c2 := open(t, "one")
	if _, ok, err := c2.Get("k"); err != nil || !ok {
		t.Fatalf("reopen mismo secret: ok=%v err=%v, esperaba true", ok, err)
	}

	// secret rotado: la cache se trunca, la clave ya no es resolubles
	c3 := open(t, "two")
	if v, ok, err := c3.Get("k"); err != nil || ok || v != nil {
		t.Fatalf("tras rotación: v=%v ok=%v err=%v, esperaba miss", v, ok, err)
	}
}

// TestHMAC verifica el contrato de hashSecret/Secret.Hash: determinista,
// distinto para claves distintas, digest SHA-256 hex de 64 chars, y
// rechazo de secret vacío.
func TestHMAC(t *testing.T) {
	secret, err := NewSecret("sekret")
	if err != nil {
		t.Fatalf("NewSecret: %v", err)
	}

	a := secret.Hash("foo")
	b := secret.Hash("foo")
	if a != b {
		t.Fatal("Hash debe ser determinista")
	}
	if a == secret.Hash("bar") {
		t.Fatal("Hash de claves distintas no debe coincidir")
	}
	if len(a) != 64 {
		t.Fatalf("Hash len=%d, esperaba 64 hex chars (SHA-256)", len(a))
	}
	if hashSecret("sekret", "foo") != a {
		t.Fatal("hashSecret(secret, key) debe coincidir con Secret.Hash(key)")
	}
	if _, err := NewSecret(""); err == nil {
		t.Fatal("NewSecret vacío debe fallar")
	}
	if _, err := NewSecret("   "); err == nil {
		t.Fatal("NewSecret solo-espacios debe fallar")
	}
}

// TestValkeyNewFromURL verifica EC-010 (URL vacía → error claro) y que una
// URL bien formada produce un cliente no nil. No conecta al servidor.
func TestValkeyNewFromURL(t *testing.T) {
	if _, err := NewFromURL(""); err == nil {
		t.Fatal("NewFromURL(\"\") debe fallar (EC-010)")
	}
	if _, err := NewFromURL("   "); err == nil {
		t.Fatal("NewFromURL espacios debe fallar (EC-010)")
	}
	if _, err := NewFromURL("not-a-url"); err == nil {
		t.Fatal("NewFromURL malformada debe fallar")
	}
	if err := RequireValkey(true, ""); err == nil {
		t.Fatal("RequireValkey(true, \"\") debe fallar (EC-010)")
	}
	if err := RequireValkey(false, ""); err != nil {
		t.Fatalf("RequireValkey(false, \"\") no debe fallar: %v", err)
	}
	client, err := NewFromURL("redis://localhost:6379/0")
	if err != nil {
		t.Fatalf("NewFromURL válida: %v", err)
	}
	if client == nil {
		t.Fatal("NewFromURL válida: cliente nil")
	}
	_ = client.Close()
}

// TestValkeyScriptConstruction verifica la construcción de los scripts Lua
// (verbatim de valkeylib.py) sin necesitar servidor: no vacíos y con los
// comandos que anuncian.
func TestValkeyScriptConstruction(t *testing.T) {
	if strings.TrimSpace(luaIncrCounter) == "" {
		t.Fatal("luaIncrCounter vacío")
	}
	if !strings.Contains(luaIncrCounter, "redis.call('GET'") ||
		!strings.Contains(luaIncrCounter, "redis.call('INCR'") {
		t.Fatal("luaIncrCounter no contiene GET/INCR")
	}

	if strings.TrimSpace(luaIncrSlidingWindow) == "" {
		t.Fatal("luaIncrSlidingWindow vacío")
	}
	for _, cmd := range []string{"ZREMRANGEBYSCORE", "ZADD", "ZCOUNT", "EXPIRE", "redis.call('TIME'"} {
		if !strings.Contains(luaIncrSlidingWindow, cmd) {
			t.Fatalf("luaIncrSlidingWindow no contiene %q", cmd)
		}
	}

	if strings.TrimSpace(luaPurgeByPrefix) == "" {
		t.Fatal("luaPurgeByPrefix vacío")
	}
	if !strings.Contains(luaPurgeByPrefix, "redis.call('KEYS'") ||
		!strings.Contains(luaPurgeByPrefix, "redis.call('EXPIRE'") {
		t.Fatal("luaPurgeByPrefix no contiene KEYS/EXPIRE")
	}
}

// TestValkeyScripts ejecuta los scripts contra un valkey real. SKIP si
// REDIS_URL no está definida (CI sin servidor): los scripts se validan
// estructuralmente en TestValkeyScriptConstruction. Documenta: la prueba
// usa una clave única por ejecución para no pisar contadores ajenos.
func TestValkeyScripts(t *testing.T) {
	url := os.Getenv("REDIS_URL")
	if url == "" {
		t.Skip("REDIS_URL no definido: skip test valkey en vivo (los scripts se validan estructuralmente)")
	}
	client, err := NewFromURL(url)
	if err != nil {
		t.Fatalf("NewFromURL: %v", err)
	}
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	prefix := fmt.Sprintf("nanuq-test-%d", time.Now().UnixNano())
	defer func() { _ = PurgeByPrefix(ctx, client, prefix) }()

	// conexión viva
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	// INCR_COUNTER: 1 → 2 → 3, con límite 3 se queda en 3
	counter := prefix + ":counter"
	for i, want := range []int64{1, 2, 3, 3} {
		n, err := IncrCounter(ctx, client, counter, 3, 60)
		if err != nil {
			t.Fatalf("IncrCounter #%d: %v", i, err)
		}
		if n != want {
			t.Fatalf("IncrCounter #%d: n=%d, esperaba %d", i, n, want)
		}
	}

	// INCR_SLIDING_WINDOW: ventana 60s, crece 1 → 2 → 3
	window := prefix + ":window"
	for i, want := range []int64{1, 2, 3} {
		n, err := IncrSlidingWindow(ctx, client, window, 60)
		if err != nil {
			t.Fatalf("IncrSlidingWindow #%d: %v", i, err)
		}
		if n != want {
			t.Fatalf("IncrSlidingWindow #%d: n=%d, esperaba %d", i, n, want)
		}
	}

	// PURGE_BY_PREFIX: tras el purge las claves expiran (miss en GET)
	if err := PurgeByPrefix(ctx, client, prefix); err != nil {
		t.Fatalf("PurgeByPrefix: %v", err)
	}
	if err := client.Get(ctx, counter).Err(); err == nil {
		t.Fatal("contador sigue existiendo tras PurgeByPrefix")
	}
}
