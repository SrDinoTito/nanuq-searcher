// hmac.go — helpers HMAC-SHA256 para derivar claves de cache.
//
// TASK-015 / REQ-014: port fiel de secret_hash de cache.py/valkeylib.py.
// Las claves almacenadas en la DB y en Valkey se derivan con HMAC-SHA256
// para que no sean legibles por terceros: "make the key stored in the DB
// unreadable for third parties" (SearXNG cache.py).
//
// Contrato (según TASK-015): hashSecret(secret, key) =
// hex(HMAC-SHA256(key=secret, msg=key)). Se sigue el contrato de la task,
// no la variante de cache.py (que usa name+password como clave HMAC y msg
// vacío) — el resultado es un digest determinista de 64 caracteres hex.
package cache

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Secret envuelve la clave secreta del servidor (cfg.Server.SecretKey).
// Un Secret se usa para derivar las claves hasheadas de la cache.
type Secret string

// NewSecret valida y envuelve la clave secreta del servidor.
//
// Devuelve error si está vacía (o solo espacios): hashear con clave vacía
// haría las claves de cache adivinables y violaría REQ-014/EC-010.
func NewSecret(secretKey string) (Secret, error) {
	if strings.TrimSpace(secretKey) == "" {
		return "", fmt.Errorf("cache: NewSecret: secret key vacía (cfg.Server.SecretKey)")
	}
	return Secret(secretKey), nil
}

// hashSecret devuelve el digest hex de HMAC-SHA256(key=secret, msg=key).
// Siempre devuelve 64 caracteres hex (32 bytes).
func hashSecret(secret, key string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(key))
	return hex.EncodeToString(mac.Sum(nil))
}

// Hash deriva la clave de cache para key: hex(HMAC-SHA256(key=secret, msg=key)).
// Método público para que los callers (p. ej. nombres de contador en valkey,
// claves de image_proxy en TASK-014) reutilicen la misma derivación.
func (s Secret) Hash(key string) string {
	return hashSecret(string(s), key)
}

// String devuelve la clave secreta (usada por la rotación de hash_token).
func (s Secret) String() string {
	return string(s)
}
