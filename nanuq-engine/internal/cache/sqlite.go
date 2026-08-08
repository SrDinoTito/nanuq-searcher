// sqlite.go — ExpireCache: cache clave/valor con expiración sobre SQLite.
//
// TASK-015 / REQ-014 / DSG-011: port de ExpireCacheSQLite de cache.py
// usando modernc.org/sqlite (driver pure-Go, SIN CGO, CA-001).
//
// Diferencias deliberadas frente al Python:
//   - Serialización: gob (encoding/gob) en lugar de pickle (gob.go).
//   - Claves en DB: hasheadas con HMAC-SHA256 + secret (Secret.Hash) en
//     lugar de la clave plana — "clave HMAC-SHA256 con secret" (TASK-015).
//   - expire_at: REAL (unix seconds con sub-segundo) en lugar de INTEGER
//     para permitir TTLs cortos (<1s) en tests y en la práctica.
//   - Una sola conexión (SetMaxOpenConns(1)): SQLite es single-writer;
//     serializar hace wal_checkpoint determinista y evita SQLITE_BUSY.
//
// Mantenimiento (port de cache.py maintenance):
//   - DELETE de filas expiradas + PRAGMA wal_checkpoint(TRUNCATE).
//   - Rotación de secret: si el hash_token de la DB no coincide con el del
//     secret actual, se truncan todas las entradas (las claves hasheadas
//     dejan de ser resolubles).
package cache

import (
	"database/sql"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // registra el driver "sqlite" (pure-Go, sin CGO)
)

const (
	// defaultHold es el TTL por defecto (7 días): MAXHOLD_TIME de cache.py.
	defaultHold = 7 * 24 * time.Hour

	// defaultMaxValueLen es el tope de tamaño del valor serializado
	// (10 KiB): MAX_VALUE_LEN de cache.py.
	defaultMaxValueLen = 10 * 1024

	// sqliteDriver es el nombre de driver registrado por modernc.org/sqlite.
	sqliteDriver = "sqlite"
)

// ExpireCache es una cache clave/valor persistente con expiración,
// respaldada por un único archivo SQLite en WAL mode.
type ExpireCache struct {
	db          *sql.DB
	secret      Secret
	hold        time.Duration
	maxValueLen int
}

// Config configura ExpireCache (TASK-015).
type Config struct {
	// Path es la ruta del archivo de base de datos SQLite.
	Path string

	// Secret deriva las claves hasheadas de la DB (cfg.Server.SecretKey).
	Secret Secret

	// Hold es el TTL por defecto cuando Put recibe ttl <= 0.
	// 0 => defaultHold (7 días).
	Hold time.Duration

	// MaxValueLen es el tope (en bytes) del valor serializado.
	// 0 => defaultMaxValueLen (10 KiB).
	MaxValueLen int
}

// Open abre (creando si no existe) la cache SQLite en cfg.Path, inicializa
// el esquema y comprueba la rotación del secret. El ExpireCache devuelto es
// seguro para uso concurrente (la pool está limitada a 1 conexión).
func Open(cfg Config) (*ExpireCache, error) {
	if cfg.Path == "" {
		return nil, fmt.Errorf("cache: Open: Path vacío")
	}
	if cfg.Secret == "" {
		return nil, fmt.Errorf("cache: Open: Secret vacío (use NewSecret)")
	}
	hold := cfg.Hold
	if hold <= 0 {
		hold = defaultHold
	}
	maxValueLen := cfg.MaxValueLen
	if maxValueLen <= 0 {
		maxValueLen = defaultMaxValueLen
	}

	db, err := sql.Open(sqliteDriver, dsn(cfg.Path))
	if err != nil {
		return nil, fmt.Errorf("cache: Open: %w", err)
	}
	// Una sola conexión: serializa acceso, evita SQLITE_BUSY en escrituras
	// y hace que PRAGMA wal_checkpoint(TRUNCATE) sea determinista.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	c := &ExpireCache{db: db, secret: cfg.Secret, hold: hold, maxValueLen: maxValueLen}
	if err := c.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return c, nil
}

// dsn construye el DSN de modernc.org/sqlite: URI file: con _pragma aplicado
// por conexión (WAL + busy_timeout + synchronous=NORMAL). filepath.ToSlash
// mantiene las rutas Windows válidas en el URI.
func dsn(path string) string {
	return "file:" + filepath.ToSlash(path) +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
}

// init crea el esquema y comprueba la rotación del secret (hash_token).
func (c *ExpireCache) init() error {
	const (
		ddlCache = `CREATE TABLE IF NOT EXISTS cache (
			key       TEXT PRIMARY KEY,
			value     BLOB,
			expire_at REAL NOT NULL
		)`
		ddlIndex = `CREATE INDEX IF NOT EXISTS cache_expire_idx ON cache(expire_at)`
		ddlProps = `CREATE TABLE IF NOT EXISTS properties (
			name  TEXT PRIMARY KEY,
			value TEXT
		)`
	)
	for _, ddl := range []string{ddlCache, ddlIndex, ddlProps} {
		if _, err := c.db.Exec(ddl); err != nil {
			return fmt.Errorf("cache: init: schema: %w", err)
		}
	}
	return c.rotateIfSecretChanged()
}

// hashTokenName es la propiedad que guarda el digest del secret.
const hashTokenName = "hash_token"

// rotateIfSecretChanged compara el digest del secret actual con el
// almacenado; si cambió, trunca la cache (port de ExpireCacheSQLite.init).
func (c *ExpireCache) rotateIfSecretChanged() error {
	// Digest del secret (no de una clave): sha256 del secret.
	token := c.secret.Hash(hashTokenName)

	var stored string
	err := c.db.QueryRow(
		"SELECT value FROM properties WHERE name = ?", hashTokenName,
	).Scan(&stored)
	if err == sql.ErrNoRows {
		// Primera apertura: almacenar el token.
		_, err = c.db.Exec(
			"INSERT INTO properties (name, value) VALUES (?, ?)",
			hashTokenName, token,
		)
		if err != nil {
			return fmt.Errorf("cache: init: almacenar hash_token: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("cache: init: leer hash_token: %w", err)
	}
	if stored == token {
		return nil
	}
	// El secret cambió: las claves hasheadas dejan de ser resolubles.
	if _, err := c.db.Exec("DELETE FROM cache"); err != nil {
		return fmt.Errorf("cache: init: truncar cache por rotación de secret: %w", err)
	}
	if _, err := c.db.Exec(
		"UPDATE properties SET value = ? WHERE name = ?",
		token, hashTokenName,
	); err != nil {
		return fmt.Errorf("cache: init: actualizar hash_token: %w", err)
	}
	slog.Warn("cache: secret rotado; cache truncada",
		"component", "cache", "db", c.dbPath())
	return nil
}

// dbPath devuelve la ruta de la DB para logs (best-effort).
func (c *ExpireCache) dbPath() string {
	var p string
	_ = c.db.QueryRow("PRAGMA database_list").Scan(new(int), &p, new(string))
	return p
}

// Put almacena value bajo key con TTL ttl. Si ttl <= 0 usa c.hold
// (defaultHold = 7 días). El valor se serializa con gob; si el payload
// supera MaxValueLen se devuelve error (port de MAX_VALUE_LEN).
//
// La clave se deriva con Secret.Hash (nunca se persiste la clave plana).
// UPSERT: INSERT ... ON CONFLICT(key) DO UPDATE (port de set de cache.py).
func (c *ExpireCache) Put(key string, value any, ttl time.Duration) error {
	if key == "" {
		return fmt.Errorf("cache: Put: key vacía")
	}
	blob, err := encodeValue(value)
	if err != nil {
		return err
	}
	if len(blob) > c.maxValueLen {
		return fmt.Errorf("cache: Put: valor serializado %d bytes excede MaxValueLen %d", len(blob), c.maxValueLen)
	}
	if ttl <= 0 {
		ttl = c.hold
	}
	expireAt := float64(time.Now().Add(ttl).UnixNano()) / 1e9
	dbKey := c.secret.Hash(key)

	_, err = c.db.Exec(`
		INSERT INTO cache (key, value, expire_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, expire_at = excluded.expire_at
	`, dbKey, blob, expireAt)
	if err != nil {
		return fmt.Errorf("cache: Put: %w", err)
	}
	return nil
}

// Set es un alias de Put (paridad con el nombre set de ExpireCacheSQLite).
func (c *ExpireCache) Set(key string, value any, ttl time.Duration) error {
	return c.Put(key, value, ttl)
}

// Get devuelve el valor bajo key. ok es false si la clave no existe o ya
// expiró. La expiración es perezosa: la fila expirada se devuelve como miss
// sin borrar ("no advantage, DELETE executed at maintenance anyway" —
// cache.py get). El valor se deserializa en un any (gob round-trip).
func (c *ExpireCache) Get(key string) (value any, ok bool, err error) {
	dbKey := c.secret.Hash(key)
	var blob []byte
	var expireAt float64
	err = c.db.QueryRow(
		"SELECT value, expire_at FROM cache WHERE key = ?", dbKey,
	).Scan(&blob, &expireAt)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("cache: Get: %w", err)
	}
	if expireAt < float64(time.Now().UnixNano())/1e9 {
		return nil, false, nil // expirada (lazy): no se borra hasta Maintenance
	}
	var v any
	if err := decodeValue(blob, &v); err != nil {
		return nil, false, err
	}
	return v, true, nil
}

// Delete elimina la entrada de key. No es error borrar una clave inexistente.
func (c *ExpireCache) Delete(key string) error {
	if _, err := c.db.Exec("DELETE FROM cache WHERE key = ?", c.secret.Hash(key)); err != nil {
		return fmt.Errorf("cache: Delete: %w", err)
	}
	return nil
}

// Maintenance borra las filas expiradas y hace wal_checkpoint(TRUNCATE)
// (port de ExpireCacheSQLite.maintenance: "vacuuming the WALs").
// Devuelve el número de filas expiradas eliminadas.
func (c *ExpireCache) Maintenance() (removed int64, err error) {
	now := float64(time.Now().UnixNano()) / 1e9
	res, err := c.db.Exec("DELETE FROM cache WHERE expire_at < ?", now)
	if err != nil {
		return 0, fmt.Errorf("cache: Maintenance: delete expiradas: %w", err)
	}
	removed, _ = res.RowsAffected()

	// wal_checkpoint(TRUNCATE): devuelve (busy, log_frames, checkpointed).
	var busy, logFrames, checkpointed int
	err = c.db.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &checkpointed)
	if err != nil {
		return removed, fmt.Errorf("cache: Maintenance: wal_checkpoint: %w", err)
	}
	if busy != 0 {
		slog.Debug("cache: wal_checkpoint con lectores activos",
			"component", "cache", "busy", busy, "log_frames", logFrames, "checkpointed", checkpointed)
	}
	return removed, nil
}

// Close cierra la base de datos. No es seguro usarla tras Close.
func (c *ExpireCache) Close() error {
	if c.db == nil {
		return nil
	}
	return c.db.Close()
}
