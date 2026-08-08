// Package handlers_test exercises GET /image_proxy (TASK-014, REQ-014)
// through the real Server.Handler() route table, mirroring webapp.py
// image_proxy(). Helpers (newTestServer, doGet) are shared with
// misc_test.go.
package handlers_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/webapp"
	"nanuq-engine/internal/webapp/handlers"
)

// pngBody son bytes falsos de imagen. No es necesario que sean un PNG
// válido: el proxy solo valida el content-type, no el contenido.
var pngBody = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00, 0x00}

// newImageProxyServer builds a Server with the image proxy enabled and a
// secret key (needed to open the cache).
func newImageProxyServer(t *testing.T) *webapp.Server {
	t.Helper()
	cfg := &config.Config{
		General: config.General{InstanceName: testInstance},
		Server:  config.Server{ImageProxy: true, SecretKey: "test-secret"},
	}
	return webapp.New(cfg, nil, nil, nil)
}

// withImageProxyCache points the image proxy cache at a fresh t.TempDir()
// file (the default path would write into the package directory) and
// restores the previous value on cleanup.
func withImageProxyCache(t *testing.T) {
	t.Helper()
	prev := handlers.ImageProxyCachePath
	handlers.ImageProxyCachePath = filepath.Join(t.TempDir(), "image_proxy.db")
	t.Cleanup(func() {
		// Cierra la conexión SQLite antes de que TempDir intente borrar el
		// archivo (en Windows un archivo abierto no se puede eliminar).
		handlers.CloseImageProxyCache()
		handlers.ImageProxyCachePath = prev
	})
}

// newImageUpstream sirve una imagen (content-type image/png) y cuenta las
// requests recibidas.
func newImageUpstream(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBody)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// TestImageProxyServesImage (a): upstream real con content-type image/png
// -> 200, Content-Type original, headers anti-XSS y body intacto.
func TestImageProxyServesImage(t *testing.T) {
	withImageProxyCache(t)
	upstream, _ := newImageUpstream(t)
	srv := newImageProxyServer(t)

	rec := doGet(t, srv, "/image_proxy?url="+url.QueryEscape(upstream.URL))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "img-src 'self'") {
		t.Errorf("Content-Security-Policy = %q, want img-src 'self'", csp)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.HasPrefix(cc, "max-age=") {
		t.Errorf("Cache-Control = %q, want max-age=...", cc)
	}
	if !bytes.Equal(rec.Body.Bytes(), pngBody) {
		t.Errorf("body = %v, want %v", rec.Body.Bytes(), pngBody)
	}
}

// TestImageProxyRejectsNonImage (b): upstream que sirve text/html -> 404
// (EC-009, el fallo no se cachea).
func TestImageProxyRejectsNonImage(t *testing.T) {
	withImageProxyCache(t)
	var hits atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, "<html>nope</html>")
	}))
	t.Cleanup(upstream.Close)
	srv := newImageProxyServer(t)

	rec := doGet(t, srv, "/image_proxy?url="+url.QueryEscape(upstream.URL))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (EC-009)", rec.Code, http.StatusNotFound)
	}
}

// TestImageProxyInvalidURL (c): url param inválido (scheme no permitido)
// -> 400.
func TestImageProxyInvalidURL(t *testing.T) {
	srv := newImageProxyServer(t)
	for _, target := range []string{
		"/image_proxy", // url vacío
		"/image_proxy?url=" + url.QueryEscape("javascript:alert(1)"), // scheme inválido
	} {
		rec := doGet(t, srv, target)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("target %q: status = %d, want %d", target, rec.Code, http.StatusBadRequest)
		}
	}
}

// TestImageProxyFTPRejected (d): scheme ftp:// -> 400.
func TestImageProxyFTPRejected(t *testing.T) {
	srv := newImageProxyServer(t)
	rec := doGet(t, srv, "/image_proxy?url="+url.QueryEscape("ftp://example.com/img.png"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// TestImageProxyDisabled (e): feature off (ImageProxy=false) -> 404, sin
// tocar la red.
func TestImageProxyDisabled(t *testing.T) {
	rec := doGet(t, newTestServer(t), "/image_proxy?url="+url.QueryEscape("https://example.com/x.png"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// TestImageProxyCacheHitSkipsUpstream (f): la segunda petición de la misma
// URL sale de la caché y NO toca el upstream (el contador sigue en 1).
func TestImageProxyCacheHitSkipsUpstream(t *testing.T) {
	withImageProxyCache(t)
	upstream, hits := newImageUpstream(t)
	srv := newImageProxyServer(t)

	target := "/image_proxy?url=" + url.QueryEscape(upstream.URL)

	rec1 := doGet(t, srv, target)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request: status = %d, want %d", rec1.Code, http.StatusOK)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("upstream hits after first request = %d, want 1", n)
	}

	rec2 := doGet(t, srv, target)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second request: status = %d, want %d", rec2.Code, http.StatusOK)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("upstream hits after cache hit = %d, want 1 (la segunda petición no debe tocar upstream)", n)
	}
	if !bytes.Equal(rec2.Body.Bytes(), pngBody) {
		t.Errorf("second body = %v, want %v", rec2.Body.Bytes(), pngBody)
	}
}
