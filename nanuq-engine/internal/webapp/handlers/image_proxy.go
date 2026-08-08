// image_proxy.go — GET /image_proxy: proxy de imágenes con caché SQLite.
//
// TASK-014 / REQ-014 / DSG-011: port de webapp.py image_proxy() (L991-1062)
// usando la ExpireCache de TASK-015 (internal/cache). Respuesta binaria
// (imagen), por lo que html/template no aplica aquí.
//
// Desviaciones deliberadas frente al Python:
//   - Sin validación HMAC del parámetro "h": el TASK-014 no la exige (solo
//     scheme http/https). La mitigación SSRF queda en la restricción de
//     scheme + el filtro de content-type image/ + el tope de tamaño.
//   - Status de error: el Python devuelve el status del upstream para
//     >=400; aquí todo error de descarga (incluido status >=400) se
//     devuelve como 404, pues el frontend asume "la imagen no está
//     disponible" (spec TASK-014).
//   - Body mayor que el máximo -> 413 (spec: "413 o 404"); el Python
//     devuelve 400.
//   - Caché: el Python no cachea (streaming directo); TASK-014 añade
//     ExpireCache con default 7 días.
package handlers

import (
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"nanuq-engine/internal/cache"
	"nanuq-engine/internal/config"
)

const (
	// imageProxyMaxSize es el tope de tamaño del body descargado (5 MiB,
	// igual que maximum_size de webapp.py image_proxy).
	imageProxyMaxSize = 5 * 1024 * 1024

	// imageProxyTimeout es el timeout del cliente HTTP de descarga (spec:
	// ~10s).
	imageProxyTimeout = 10 * time.Second

	// imageProxyMaxRedirects limita los redirects seguidos. Go permite 10
	// por defecto; "máx razonable" (spec) -> 5.
	imageProxyMaxRedirects = 5

	// imageProxyCacheMaxValueLen permite cachear imágenes de hasta
	// imageProxyMaxSize. El defaultMaxValueLen de TASK-015 es 10 KiB, que
	// no alcanza para imágenes; esta cache se abre con su propio tope.
	imageProxyCacheMaxValueLen = 5 * 1024 * 1024

	// imageProxyUserAgent identifica el proxy ante los servidores de
	// imágenes (perfil de headers similar a gen_useragent() del Python).
	imageProxyUserAgent = "Mozilla/5.0 (X11; Linux x86_64; rv:115.0) Gecko/20100101 Firefox/115.0"

	// imageProxyCacheControl es el Cache-Control de la respuesta (1 día).
	imageProxyCacheControl = "max-age=86400"
)

// ImageProxyCachePath es la ruta del archivo SQLite de la caché de
// /image_proxy. El default es relativo al working directory:
// "cache/image_proxy.db" (misma convención que el dir cache/ de SearXNG).
//
// Se lee en cada request (la apertura es perezosa, en la primera request de
// cada server), por lo que los tests pueden apuntarla a un t.TempDir()
// antes de construir el server.
//
// DESVIACIÓN: el spec menciona "cfg.Cache.Path" pero TASK-015 no añadió un
// campo Cache a internal/config (solo existe cache.Config en internal/
// cache). Se usa el default fijo; cuando cfg.Cache exista, esta variable
// pasará a derivarse de él.
var ImageProxyCachePath = filepath.Join("cache", "image_proxy.db")

// imageProxyCachesMu protege imageProxyCaches (registro de las cachés
// abiertas por este proceso).
var (
	imageProxyCachesMu sync.Mutex
	imageProxyCaches   = map[*cache.ExpireCache]struct{}{}
)

// CloseImageProxyCache cierra todas las cachés de /image_proxy abiertas
// por este proceso y limpia el registro.
//
// En producción la caché vive tanto como el proceso (una conexión SQLite
// long-lived es lo deseable), por lo que no hace falta llamarla. Existe
// para que los tests (paquete externo handlers_test) liberen los archivos
// SQLite antes del cleanup de t.TempDir(), y como gancho para un futuro
// shutdown graceful.
func CloseImageProxyCache() {
	imageProxyCachesMu.Lock()
	defer imageProxyCachesMu.Unlock()
	for c := range imageProxyCaches {
		_ = c.Close()
	}
	imageProxyCaches = map[*cache.ExpireCache]struct{}{}
}

// imageProxyCacheValue es el valor cacheado: content-type + bytes del body.
//
// gob exige registro (gob.Register) para los tipos concretos que viajan a
// través de interfaces (Get devuelve any) — contrato documentado en
// cache/gob.go. Se registra en init() de este paquete.
type imageProxyCacheValue struct {
	ContentType string
	Body        []byte
}

func init() { gob.Register(imageProxyCacheValue{}) }

// errImageProxyTooLarge marca un body (o Content-Length) que excede
// imageProxyMaxSize. Se mapea a 413; el resto de errores de descarga se
// mapean a 404.
var errImageProxyTooLarge = errors.New("image_proxy: la imagen excede el tamaño máximo")

// RegisterImageProxy wires GET /image_proxy (TASK-014, REQ-014).
func RegisterImageProxy(mux *http.ServeMux, cfg *config.Config) {
	mux.HandleFunc("/image_proxy", imageProxyHandler(cfg))
}

// imageProxyHandler sirve GET /image_proxy?url=<encoded>.
//
// Flujo (port de webapp.py image_proxy, L991-1062, según spec TASK-014):
//
//  1. cfg.Server.ImageProxy == false -> 404 (feature off).
//  2. url vacío -> 400. url.Parse falla o scheme ∉ {http, https} -> 400.
//  3. Cache hit -> se sirve sin red (body + content-type cacheados).
//  4. Miss: GET url con http.Client{Timeout: 10s}, redirects ≤ 5.
//  5. status >= 400 -> 404. Content-Type no "image/" -> 404 (EC-009,
//     sin cachear el fallo).
//  6. Body > 5 MiB (por Content-Length o lectura) -> 413, sin cachear.
//  7. Cachea body + content-type (defaultHold 7 días) y sirve con headers
//     anti-XSS: X-Content-Type-Options: nosniff, Content-Security-Policy
//     restringida, Cache-Control.
//
// Los errores de descarga se devuelven como 404 (el frontend asume que la
// imagen no está disponible); nunca 500. Si la caché no está disponible
// (error al abrir), se degrada a no-cache con slog.Warn.
//
// La caché se abre perezosamente en la primera request y se comparte entre
// requests del mismo server (sync.Once por closure). Cada server (webapp.
// New) obtiene la suya.
func imageProxyHandler(cfg *config.Config) http.HandlerFunc {
	var (
		cacheOnce    sync.Once
		imageCache   *cache.ExpireCache
		cacheOpenErr error
	)
	return func(w http.ResponseWriter, r *http.Request) {
		if !cfg.Server.ImageProxy {
			http.NotFound(w, r)
			return
		}

		rawURL := r.FormValue("url")
		if rawURL == "" {
			http.Error(w, "missing url", http.StatusBadRequest)
			return
		}
		u, err := url.Parse(rawURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			http.Error(w, "invalid url", http.StatusBadRequest)
			return
		}

		cacheOnce.Do(func() {
			imageCache, cacheOpenErr = openImageProxyCache(cfg)
			if cacheOpenErr != nil {
				slog.Warn("image_proxy: caché no disponible; degradando a no-cache",
					"path", ImageProxyCachePath, "err", cacheOpenErr)
			}
		})

		var cached *imageProxyCacheValue
		if cacheOpenErr == nil {
			if v, ok, err := imageCache.Get(rawURL); err != nil {
				// Fallo de caché: no romper la request, re-descargar.
				slog.Warn("image_proxy: cache get fallido; degradando a no-cache", "err", err)
			} else if ok {
				if c, isCached := v.(imageProxyCacheValue); isCached {
					cached = &c
				} else {
					slog.Warn("image_proxy: valor cacheado de tipo inesperado; re-descargando")
				}
			}
		}
		if cached != nil {
			writeImageProxyResponse(w, cached.ContentType, cached.Body)
			return
		}

		body, contentType, err := downloadImage(u.String())
		if err != nil {
			if errors.Is(err, errImageProxyTooLarge) {
				http.Error(w, "image too large", http.StatusRequestEntityTooLarge)
				return
			}
			// EC-009: no cachear el resultado fallido.
			slog.Debug("image_proxy: descarga fallida", "url", u.String(), "err", err)
			http.NotFound(w, r)
			return
		}

		if cacheOpenErr == nil {
			if err := imageCache.Put(rawURL, imageProxyCacheValue{ContentType: contentType, Body: body}, 0); err != nil {
				slog.Warn("image_proxy: cache put fallido", "url", rawURL, "err", err)
			}
		}

		writeImageProxyResponse(w, contentType, body)
	}
}

// openImageProxyCache abre la ExpireCache de /image_proxy (TASK-015). El
// secret deriva de cfg.Server.SecretKey (cache.NewSecret). MaxValueLen se
// sube al tope de descarga para que las imágenes quepan (el default de
// TASK-015 es 10 KiB). Hold = 0 -> defaultHold (7 días).
func openImageProxyCache(cfg *config.Config) (*cache.ExpireCache, error) {
	secret, err := cache.NewSecret(cfg.Server.SecretKey)
	if err != nil {
		return nil, err
	}
	c, err := cache.Open(cache.Config{
		Path:        ImageProxyCachePath,
		Secret:      secret,
		MaxValueLen: imageProxyCacheMaxValueLen,
	})
	if err != nil {
		return nil, err
	}
	imageProxyCachesMu.Lock()
	imageProxyCaches[c] = struct{}{}
	imageProxyCachesMu.Unlock()
	return c, nil
}

// downloadImage descarga u con timeout, siguiendo redirects (≤ 5) y
// validando status, content-type y tamaño. Devuelve error para status
// >= 400, content-type no "image/" (EC-009), body > imageProxyMaxSize
// (errImageProxyTooLarge -> 413) y cualquier fallo de red. El caller no
// cachea el fallo.
//
// No se envía Accept-Encoding: el http.Transport añade gzip por defecto y
// descomprime transparentemente, eliminando el header Content-Encoding del
// body que servimos.
func downloadImage(u string) ([]byte, string, error) {
	client := &http.Client{
		Timeout: imageProxyTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= imageProxyMaxRedirects {
				return fmt.Errorf("image_proxy: demasiados redirects (%d)", len(via))
			}
			return nil
		},
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	// Mismo perfil de headers que webapp.py image_proxy (L1005-1010).
	req.Header.Set("User-Agent", imageProxyUserAgent)
	req.Header.Set("Accept", "image/webp,*/*")
	req.Header.Set("Sec-GPC", "1")
	req.Header.Set("DNT", "1")

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, "", fmt.Errorf("image_proxy: status %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "image/") {
		return nil, "", fmt.Errorf("image_proxy: content-type no imagen %q (EC-009)", contentType)
	}

	// Check temprano de Content-Length: evita leer un body gigante.
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		if n, err := strconv.ParseInt(cl, 10, 64); err == nil && n > imageProxyMaxSize {
			return nil, "", errImageProxyTooLarge
		}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, imageProxyMaxSize+1))
	if err != nil {
		return nil, "", err
	}
	if len(body) > imageProxyMaxSize {
		return nil, "", errImageProxyTooLarge
	}
	return body, contentType, nil
}

// writeImageProxyResponse escribe la imagen con los headers anti-XSS
// (X-Content-Type-Options: nosniff, CSP restringida) y Cache-Control.
// Se usa tanto para respuestas frescas como para cache hits.
func writeImageProxyResponse(w http.ResponseWriter, contentType string, body []byte) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Content-Length", strconv.Itoa(len(body)))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data:")
	h.Set("Cache-Control", imageProxyCacheControl)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
