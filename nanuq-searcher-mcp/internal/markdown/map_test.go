package markdown

import (
	"strings"
	"testing"

	"nanuq-searcher-mcp/internal/domain"
)

func sampleSiteMap() domain.SiteMap {
	return domain.SiteMap{
		RootURL: "https://example.com/",
		Pages: []domain.Page{
			{
				URL:   "https://example.com/",
				Title: "Inicio",
				Depth: 0,
				Headings: []domain.Heading{
					{Level: 1, Text: "Bienvenido"},
					{Level: 2, Text: "Subsección"},
					{Level: 3, Text: "Detalle"},
				},
			},
			{
				URL:   "https://example.com/acerca",
				Title: "Acerca de",
				Depth: 1,
				Headings: []domain.Heading{
					{Level: 2, Text: "Historia"},
				},
			},
		},
		Visited: 2,
	}
}

func TestRenderMapHeaderAndStats(t *testing.T) {
	out := RenderMap(sampleSiteMap())
	if !strings.HasPrefix(out, "# Mapa de https://example.com/\n\n") {
		t.Errorf("missing header, got:\n%s", out)
	}
	if !strings.Contains(out, "Visitas: 2 · Cancelado: no") {
		t.Errorf("missing stats line, got:\n%s", out)
	}
}

func TestRenderMapCancelled(t *testing.T) {
	sm := sampleSiteMap()
	sm.Cancelled = true
	out := RenderMap(sm)
	if !strings.Contains(out, "Cancelado: sí") {
		t.Errorf("missing 'Cancelado: sí', got:\n%s", out)
	}
}

func TestRenderMapHostErrorsSorted(t *testing.T) {
	sm := sampleSiteMap()
	sm.HostErrors = map[string]string{
		"zeta.example":  "HTTP 500 Internal Server Error",
		"alpha.example": "HTTP 503 Service Unavailable",
	}
	out := RenderMap(sm)
	// Keys must be sorted: alpha before zeta.
	i := strings.Index(out, "alpha.example")
	j := strings.Index(out, "zeta.example")
	if i < 0 || j < 0 || i > j {
		t.Errorf("host errors not sorted (alpha before zeta), got:\n%s", out)
	}
	if !strings.Contains(out, "Errores de host: alpha.example (HTTP 503 Service Unavailable), zeta.example (HTTP 500 Internal Server Error)") {
		t.Errorf("wrong host errors rendering, got:\n%s", out)
	}
}

func TestRenderMapTreeIndentation(t *testing.T) {
	sm := domain.SiteMap{
		RootURL: "https://example.com/",
		Pages: []domain.Page{
			{URL: "https://example.com/", Title: "Nivel 0", Depth: 0},
			{URL: "https://example.com/a", Title: "Nivel 1", Depth: 1},
			{URL: "https://example.com/a/b", Title: "Nivel 2", Depth: 2},
		},
	}
	out := RenderMap(sm)
	if !strings.Contains(out, "- [Nivel 0](") {
		t.Errorf("depth 0 bullet wrong, got:\n%s", out)
	}
	if !strings.Contains(out, "  - [Nivel 1](") {
		t.Errorf("depth 1 bullet should be indented 2 spaces, got:\n%s", out)
	}
	if !strings.Contains(out, "    - [Nivel 2](") {
		t.Errorf("depth 2 bullet should be indented 4 spaces, got:\n%s", out)
	}
}

func TestRenderMapOutlineHeadingLevels(t *testing.T) {
	sm := domain.SiteMap{
		RootURL: "https://example.com/",
		Pages: []domain.Page{
			{
				URL:   "https://example.com/",
				Title: "Página",
				Headings: []domain.Heading{
					{Level: 1, Text: "Título H1"},
					{Level: 2, Text: "Sección H2"},
					{Level: 3, Text: "Subsección H3"},
					{Level: 4, Text: "H4 debe ignorarse"},
					{Level: 2, Text: "   "}, // whitespace-only must be skipped
				},
			},
		},
	}
	out := RenderMap(sm)
	// hash count = Level + 2: H1 → ###, H2 → ####, H3 → #####.
	if !strings.Contains(out, "### Título H1") {
		t.Errorf("missing '### Título H1', got:\n%s", out)
	}
	if !strings.Contains(out, "#### Sección H2") {
		t.Errorf("missing '#### Sección H2', got:\n%s", out)
	}
	if !strings.Contains(out, "##### Subsección H3") {
		t.Errorf("missing '##### Subsección H3', got:\n%s", out)
	}
	if strings.Contains(out, "H4 debe ignorarse") {
		t.Errorf("Level 4 heading must be skipped, got:\n%s", out)
	}
}

func TestRenderMapPageErrors(t *testing.T) {
	sm := domain.SiteMap{
		RootURL: "https://example.com/",
		Pages: []domain.Page{
			{
				URL:    "https://example.com/",
				Title:  "Página",
				Errors: []string{"HTTP 500 Internal Server Error", "  timeout  "},
			},
		},
	}
	out := RenderMap(sm)
	if !strings.Contains(out, "- [Página](https://example.com/) — error: HTTP 500 Internal Server Error; timeout") {
		t.Errorf("error note wrong, got:\n%s", out)
	}
}

func TestRenderMapDefensiveEmptyTitleAndURL(t *testing.T) {
	sm := domain.SiteMap{
		RootURL: "https://example.com/",
		Pages: []domain.Page{
			{URL: "https://example.com/no-title", Title: ""},
			{URL: "", Title: "Sin URL"},
		},
	}
	out := RenderMap(sm)
	// Empty title → URL rendered as link text.
	if !strings.Contains(out, "- [https://example.com/no-title](https://example.com/no-title)") {
		t.Errorf("empty title must render URL as text, got:\n%s", out)
	}
	// Empty URL → plain title text.
	if !strings.Contains(out, "- Sin URL") {
		t.Errorf("empty URL must render plain title, got:\n%s", out)
	}
}

func TestRenderMapEmptySiteMap(t *testing.T) {
	out := RenderMap(domain.SiteMap{})
	if !strings.Contains(out, "(sin URL raíz)") {
		t.Errorf("empty RootURL must render fallback, got:\n%s", out)
	}
	if !strings.Contains(out, "Visitas: 0 · Cancelado: no") {
		t.Errorf("empty stats wrong, got:\n%s", out)
	}
}

func TestRenderMapTrailingNewlineInvariant(t *testing.T) {
	for _, sm := range []domain.SiteMap{
		domain.SiteMap{RootURL: "https://example.com/"},
		sampleSiteMap(),
		domain.SiteMap{},
	} {
		out := RenderMap(sm)
		if !strings.HasSuffix(out, "\n") || strings.HasSuffix(out, "\n\n") {
			t.Errorf("output must end in exactly one newline, got %q", out)
		}
	}
}

func TestRenderMapContentEmbedsPageContent(t *testing.T) {
	sm := sampleSiteMap()
	sm.Pages[0].Content = "Párrafo del contenido de la página\ncon segunda línea\n"
	sm.Pages[1].Content = "   " // whitespace-only → skipped

	out := RenderMapContent(sm)
	if !strings.Contains(out, "**Contenido:**") {
		t.Errorf("missing content marker, got:\n%s", out)
	}
	if !strings.Contains(out, "Párrafo del contenido de la página\ncon segunda línea") {
		t.Errorf("missing page content, got:\n%s", out)
	}
	// Whitespace-only content must not produce an empty block.
	if strings.Count(out, "**Contenido:**") != 1 {
		t.Errorf("whitespace-only content must be skipped, got %d markers:\n%s", strings.Count(out, "**Contenido:**"), out)
	}
}

func TestRenderMapWithoutContentHasNoContentBlocks(t *testing.T) {
	sm := sampleSiteMap()
	sm.Pages[0].Content = "contenido que no debe aparecer"
	out := RenderMap(sm)
	if strings.Contains(out, "**Contenido:**") || strings.Contains(out, "contenido que no debe aparecer") {
		t.Errorf("RenderMap must not emit content, got:\n%s", out)
	}
}

func TestRenderMapContentWithTrailingNewlineContent(t *testing.T) {
	sm := domain.SiteMap{
		RootURL: "https://example.com/",
		Pages: []domain.Page{
			{URL: "https://example.com/", Title: "Página", Content: "texto\n\n"},
		},
	}
	out := RenderMapContent(sm)
	if strings.HasSuffix(out, "\n\n\n") {
		t.Errorf("trailing content newlines must be trimmed, got %q", out)
	}
	if !strings.Contains(out, "**Contenido:**\n\ntexto\n") {
		t.Errorf("content block wrong, got:\n%s", out)
	}
	if !strings.HasSuffix(out, "\n") || strings.HasSuffix(out, "\n\n") {
		t.Errorf("output must still end in exactly one newline, got %q", out)
	}
}
