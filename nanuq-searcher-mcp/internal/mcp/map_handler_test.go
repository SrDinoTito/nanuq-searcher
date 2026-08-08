package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"nanuq-searcher-mcp/internal/config"
)

// newMapTestServer builds a Server with the given MCP config and no service
// (nanuq_map does not need the search engine).
func newMapTestServer(cfg *config.Config) *mcpserver.MCPServer {
	return NewServer(nil, cfg, discardLogger())
}

// callMap invokes the nanuq_map handler with the given context and arguments
// and returns the result, failing the test on protocol-level errors.
func callMap(t *testing.T, srv *mcpserver.MCPServer, ctx context.Context, args map[string]any) *mcpgo.CallToolResult {
	t.Helper()
	st := srv.GetTool(ToolMap)
	if st == nil {
		t.Fatal("tool nanuq_map not registered")
	}
	result, err := st.Handler(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: ToolMap, Arguments: args},
	})
	if err != nil {
		t.Fatalf("handleMap returned error: %v", err)
	}
	if result == nil {
		t.Fatal("handleMap returned nil result")
	}
	return result
}

// mapHTML returns a minimal HTML page: a title, an h1 heading, recognizable
// body text (inside <main> so the fallback extractor finds it) and optional
// links.
func mapHTML(title, heading, body string, links []string) string {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html lang="es"><head><meta charset="utf-8"><title>`)
	b.WriteString(title)
	b.WriteString(`</title></head><body><main><h1>`)
	b.WriteString(heading)
	b.WriteString(`</h1><p>`)
	b.WriteString(body)
	b.WriteString(`</p>`)
	for _, l := range links {
		b.WriteString(`<a href="`)
		b.WriteString(l)
		b.WriteString(`">link</a>`)
	}
	b.WriteString(`</main></body></html>`)
	return b.String()
}

// TestHandleMapCrawlsSite renders a 3-page site (root -> B -> C) and asserts
// the map contains root and B but not C (C is dropped by the max_pages cap;
// BFS chain keeps the outcome deterministic: C is enqueued only after the
// visited slot is exhausted).
func TestHandleMapCrawlsSite(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(mapHTML("Página A", "Bienvenido", "Texto de la página raíz", []string{"/b"})))
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(mapHTML("Página B", "Sección B", "Texto de la página B", []string{"/c"})))
	})
	mux.HandleFunc("/c", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(mapHTML("Página C", "Sección C", "Texto de la página C", nil)))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := newMapTestServer(nil)
	args := map[string]any{
		"url":       srv.URL + "/",
		"max_pages": float64(2),
		"max_depth": float64(2),
		"same_host": true,
	}
	result := callMap(t, s, context.Background(), args)
	if result.IsError {
		t.Fatalf("unexpected isError: %s", searchText(t, result))
	}
	text := searchText(t, result)

	if !strings.Contains(text, "# Mapa de "+srv.URL+"/") {
		t.Errorf("missing map header in:\n%s", text)
	}
	if !strings.Contains(text, "Visitas: 2") {
		t.Errorf("missing 'Visitas: 2' in:\n%s", text)
	}
	if !strings.Contains(text, "Cancelado: no") {
		t.Errorf("missing 'Cancelado: no' in:\n%s", text)
	}
	if !strings.Contains(text, "- [Página A](") {
		t.Errorf("missing root page bullet in:\n%s", text)
	}
	if !strings.Contains(text, "- [Página B](") {
		t.Errorf("missing page B bullet in:\n%s", text)
	}
	if strings.Contains(text, "Página C") {
		t.Errorf("page C should be dropped by max_pages=2, got:\n%s", text)
	}
	// Outline headings: h1 renders at level 1+2 = ###.
	if !strings.Contains(text, "### Bienvenido") {
		t.Errorf("missing outline heading '### Bienvenido' in:\n%s", text)
	}
	if !strings.Contains(text, "### Sección B") {
		t.Errorf("missing outline heading '### Sección B' in:\n%s", text)
	}
	if !strings.HasSuffix(text, "\n") || strings.HasSuffix(text, "\n\n") {
		t.Errorf("output must end in exactly one newline, got:\n%q", text)
	}
}

// TestHandleMapSameHostDefaultExcludesExternalLink renders a root page that
// links to a second server; with same_host=true (the tool default, REQ-011)
// the external page must not appear in the map.
func TestHandleMapSameHostDefaultExcludesExternalLink(t *testing.T) {
	ext := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(mapHTML("Página Ext", "Externa", "Contenido externo", nil)))
	}))
	defer ext.Close()

	root := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(mapHTML("Página A", "Raíz", "Contenido raíz", []string{ext.URL + "/"})))
	}))
	defer root.Close()

	s := newMapTestServer(nil)
	result := callMap(t, s, context.Background(), map[string]any{"url": root.URL + "/"})
	text := searchText(t, result)
	if strings.Contains(text, "Página Ext") {
		t.Errorf("external page must be excluded with same_host=true, got:\n%s", text)
	}
	if !strings.Contains(text, "Página A") {
		t.Errorf("root page missing in:\n%s", text)
	}
}

// TestHandleMapSameHostFalseIncludesExternalLink is the same setup with
// same_host=false: the external page is now part of the map.
func TestHandleMapSameHostFalseIncludesExternalLink(t *testing.T) {
	ext := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(mapHTML("Página Ext", "Externa", "Contenido externo", nil)))
	}))
	defer ext.Close()

	root := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(mapHTML("Página A", "Raíz", "Contenido raíz", []string{ext.URL + "/"})))
	}))
	defer root.Close()

	s := newMapTestServer(nil)
	result := callMap(t, s, context.Background(), map[string]any{
		"url":       root.URL + "/",
		"same_host": false,
	})
	text := searchText(t, result)
	if !strings.Contains(text, "Página Ext") {
		t.Errorf("external page must be included with same_host=false, got:\n%s", text)
	}
}

// TestHandleMapRespectsRobotsDisallow serves a robots.txt that disallows
// /secret; the crawler must skip it (REQ-013).
func TestHandleMapRespectsRobotsDisallow(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("User-agent: *\nDisallow: /secret\n"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(mapHTML("Página A", "Raíz", "Contenido raíz", []string{"/secret"})))
	})
	mux.HandleFunc("/secret", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(mapHTML("Página Oculta", "Secreta", "No debe aparecer", nil)))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := newMapTestServer(nil)
	result := callMap(t, s, context.Background(), map[string]any{"url": srv.URL + "/"})
	text := searchText(t, result)
	if strings.Contains(text, "Página Oculta") || strings.Contains(text, "/secret") {
		t.Errorf("disallowed page must be skipped, got:\n%s", text)
	}
	if !strings.Contains(text, "Página A") {
		t.Errorf("allowed root page missing in:\n%s", text)
	}
}

// TestHandleMapMaxDepthCapsExploration: root -> B -> C with max_depth=1; C is
// at depth 2 and must be dropped while root and B are kept.
func TestHandleMapMaxDepthCapsExploration(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(mapHTML("Página A", "Raíz", "Contenido raíz", []string{"/b"})))
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(mapHTML("Página B", "Sección B", "Contenido B", []string{"/c"})))
	})
	mux.HandleFunc("/c", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(mapHTML("Página C", "Sección C", "Contenido C", nil)))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := newMapTestServer(nil)
	result := callMap(t, s, context.Background(), map[string]any{
		"url":       srv.URL + "/",
		"max_depth": float64(1),
	})
	text := searchText(t, result)
	if !strings.Contains(text, "Página A") || !strings.Contains(text, "Página B") {
		t.Errorf("root and B must be present, got:\n%s", text)
	}
	if strings.Contains(text, "Página C") {
		t.Errorf("page C is beyond max_depth=1, got:\n%s", text)
	}
}

// TestHandleMapIncludeContentEnrichesPages asserts that with include_content
// the page content is re-fetched and rendered under "**Contenido:**".
func TestHandleMapIncludeContentEnrichesPages(t *testing.T) {
	body := "Este es el texto reconocible de la página de contenido"
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(mapHTML("Página A", "Raíz", body, nil)))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := newMapTestServer(fetchDefaultConfig())
	result := callMap(t, s, context.Background(), map[string]any{
		"url":             srv.URL + "/",
		"include_content": true,
	})
	text := searchText(t, result)
	if !strings.Contains(text, "**Contenido:**") {
		t.Errorf("missing content block, got:\n%s", text)
	}
	if !strings.Contains(text, body) {
		t.Errorf("missing recognizable body text, got:\n%s", text)
	}
	// Without include_content the same crawl must NOT render content blocks.
	plain := callMap(t, s, context.Background(), map[string]any{"url": srv.URL + "/"})
	ptext := searchText(t, plain)
	if strings.Contains(ptext, "**Contenido:**") {
		t.Errorf("include_content=false must not render content, got:\n%s", ptext)
	}
}

// TestHandleMapCancelledContextReportsCancelled passes an already-cancelled
// context: the crawl must return an empty partial map flagged as cancelled.
func TestHandleMapCancelledContextReportsCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(mapHTML("Página A", "Raíz", "Contenido raíz", nil)))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s := newMapTestServer(nil)
	result := callMap(t, s, ctx, map[string]any{"url": srv.URL + "/"})
	if result.IsError {
		t.Fatalf("cancellation is not an input error, got isError: %s", searchText(t, result))
	}
	text := searchText(t, result)
	if !strings.Contains(text, "Cancelado: sí") {
		t.Errorf("missing 'Cancelado: sí' in:\n%s", text)
	}
	if !strings.Contains(text, "Visitas: 0") {
		t.Errorf("cancelled crawl should visit 0 pages, got:\n%s", text)
	}
}

// TestHandleMapHTTP500RecordsHostError: a root URL answering 500 must land in
// HostErrors (host stopped) and on the page bullet as an error note.
func TestHandleMapHTTP500RecordsHostError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := newMapTestServer(nil)
	result := callMap(t, s, context.Background(), map[string]any{"url": srv.URL + "/"})
	if result.IsError {
		t.Fatalf("server errors are domain errors, not input errors: %s", searchText(t, result))
	}
	text := searchText(t, result)
	if !strings.Contains(text, "Errores de host:") {
		t.Errorf("missing host errors in:\n%s", text)
	}
	if !strings.Contains(text, "HTTP 500") {
		t.Errorf("missing HTTP 500 detail in:\n%s", text)
	}
	if !strings.Contains(text, "— error:") {
		t.Errorf("missing page error note in:\n%s", text)
	}
}

// TestHandleMapInvalidArgumentsIsError exercises every input-validation
// branch of handleMap: all must be protocol errors (isError).
func TestHandleMapInvalidArgumentsIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	s := newMapTestServer(nil)
	cases := []struct {
		name string
		args map[string]any
	}{
		{"missing url", map[string]any{}},
		{"empty url", map[string]any{"url": "   "}},
		{"non-string url", map[string]any{"url": 42}},
		{"unnormalizable url", map[string]any{"url": "no-scheme"}},
		{"max_pages too low", map[string]any{"url": srv.URL, "max_pages": float64(0)}},
		{"max_pages too high", map[string]any{"url": srv.URL, "max_pages": float64(10001)}},
		{"max_pages non-number", map[string]any{"url": srv.URL, "max_pages": "100"}},
		{"max_depth too low", map[string]any{"url": srv.URL, "max_depth": float64(0)}},
		{"max_depth too high", map[string]any{"url": srv.URL, "max_depth": float64(11)}},
		{"max_depth non-number", map[string]any{"url": srv.URL, "max_depth": true}},
		{"same_host non-bool", map[string]any{"url": srv.URL, "same_host": "yes"}},
		{"respect_robots non-bool", map[string]any{"url": srv.URL, "respect_robots": 1}},
		{"include_content non-bool", map[string]any{"url": srv.URL, "include_content": "true"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := callMap(t, s, context.Background(), tc.args)
			if !result.IsError {
				t.Errorf("expected isError for %s, got:\n%s", tc.name, searchText(t, result))
			}
		})
	}
}
