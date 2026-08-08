package markdown

import (
	"strings"
	"testing"

	"nanuq-searcher-mcp/internal/domain"
)

// hit builds a SearchHit quickly for table-driven tests.
func hit(title, content, url string, engines []string, score float64, category string) domain.SearchHit {
	return domain.SearchHit{Title: title, Content: content, URL: url, Engines: engines, Score: score, Category: category}
}

func TestRenderSearchSingleHit(t *testing.T) {
	tests := []struct {
		name     string
		res      domain.SearchResult
		prefix   string
		wantSubs []string
	}{
		{
			name: "full hit",
			res: domain.SearchResult{
				Query: "golang",
				Hits: []domain.SearchHit{
					hit("Go Programming Language", "The Go Programming Language is an open source project.",
						"https://go.dev/", []string{"duckduckgo", "wikipedia"}, 0.9876, "general"),
				},
			},
			prefix: "## Resultados para \"golang\"\n\n1. ",
			wantSubs: []string{
				"[Go Programming Language](https://go.dev/)",
				"— The Go Programming Language is an open source project.",
				"engines: duckduckgo, wikipedia",
				"score: 0.988",
				"categoria: general",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderSearch(tt.res)
			if !strings.HasPrefix(got, tt.prefix) {
				t.Errorf("output must start with %q\n--- got ---\n%s", tt.prefix, got)
			}
			for _, sub := range tt.wantSubs {
				if !strings.Contains(got, sub) {
					t.Errorf("output missing %q\n--- got ---\n%s", sub, got)
				}
			}
		})
	}
}

func TestRenderSearchMultipleHitsOrdered(t *testing.T) {
	res := domain.SearchResult{
		Query: "go",
		Hits: []domain.SearchHit{
			hit("First", "content one", "https://a.example/", nil, 0.3, ""),
			hit("Second", "content two", "https://b.example/", nil, 0.2, ""),
			hit("Third", "content three", "https://c.example/", nil, 0.1, ""),
		},
	}
	got := RenderSearch(res)
	one := strings.Index(got, "1. [First](https://a.example/)")
	two := strings.Index(got, "2. [Second](https://b.example/)")
	three := strings.Index(got, "3. [Third](https://c.example/)")
	if one == -1 || two == -1 || three == -1 {
		t.Fatalf("missing numbered hits\n--- got ---\n%s", got)
	}
	if one >= two || two >= three {
		t.Errorf("hits not in 1., 2., 3. order (indexes %d, %d, %d)", one, two, three)
	}
}

func TestRenderSearchNoHits(t *testing.T) {
	got := RenderSearch(domain.SearchResult{Query: "qwerty12345"})
	for _, sub := range []string{
		"## Resultados para \"qwerty12345\"",
		"Sin resultados para \"qwerty12345\".",
		"Sugerencia: reintenta con otras palabras.",
	} {
		if !strings.Contains(got, sub) {
			t.Errorf("output missing %q\n--- got ---\n%s", sub, got)
		}
	}
}

func TestRenderSearchEmptyQuery(t *testing.T) {
	got := RenderSearch(domain.SearchResult{})
	for _, sub := range []string{
		"## Búsqueda vacía",
		"Escribe una consulta para buscar en la web (ej.: \"golang\").",
	} {
		if !strings.Contains(got, sub) {
			t.Errorf("output missing %q\n--- got ---\n%s", sub, got)
		}
	}
	if strings.Contains(got, "Sin resultados") {
		t.Errorf("empty query must not render as a no-results error\n--- got ---\n%s", got)
	}
}

func TestRenderSearchUnresponsive(t *testing.T) {
	res := domain.SearchResult{
		Query: "go",
		Hits: []domain.SearchHit{
			hit("Hit", "content", "https://a.example/", []string{"duckduckgo"}, 0.5, ""),
		},
		Unresponsive: []string{"brave", "bing"},
	}
	got := RenderSearch(res)
	for _, sub := range []string{
		"## ⚠️ Sin respuesta: brave, bing",
		"- brave",
		"- bing",
	} {
		if !strings.Contains(got, sub) {
			t.Errorf("output missing %q\n--- got ---\n%s", sub, got)
		}
	}
	if strings.Index(got, "- bing") < strings.Index(got, "1. [Hit](https://a.example/)") {
		t.Errorf("unresponsive section must come after the hits\n--- got ---\n%s", got)
	}
}

func TestRenderSearchRedirect(t *testing.T) {
	res := domain.SearchResult{
		Query:       "golang",
		RedirectURL: "https://duckduckgo.com/?q=golang",
	}
	got := RenderSearch(res)
	if !strings.HasPrefix(got, "> ⚠️ Bang externo detectado → [URL](https://duckduckgo.com/?q=golang)\n") {
		t.Errorf("bang notice must be the first line\n--- got ---\n%s", got)
	}
}

func TestRenderSearchTrailingNewline(t *testing.T) {
	tests := []struct {
		name string
		res  domain.SearchResult
	}{
		{"empty query", domain.SearchResult{}},
		{"no hits", domain.SearchResult{Query: "x"}},
		{"single hit", domain.SearchResult{Query: "x", Hits: []domain.SearchHit{hit("T", "c", "https://a.example/", nil, 0, "")}}},
		{"unresponsive", domain.SearchResult{Query: "x", Unresponsive: []string{"brave"}}},
		{"redirect", domain.SearchResult{Query: "x", RedirectURL: "https://d.example/"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderSearch(tt.res)
			if !strings.HasSuffix(got, "\n") {
				t.Errorf("output must end with a trailing newline\n--- got ---\n%q", got)
			}
			if strings.HasSuffix(got, "\n\n") {
				t.Errorf("output must not end with a double newline\n--- got ---\n%q", got)
			}
		})
	}
}

func TestRenderSearchMinimalMetadata(t *testing.T) {
	res := domain.SearchResult{
		Query: "x",
		Hits:  []domain.SearchHit{hit("T", "c", "https://a.example/", nil, 0.25, "")},
	}
	got := RenderSearch(res)
	if !strings.Contains(got, "   - score: 0.250") {
		t.Errorf("missing score-only metadata line\n--- got ---\n%s", got)
	}
	if strings.Contains(got, "engines:") || strings.Contains(got, "categoria:") {
		t.Errorf("empty engines/category must not be emitted\n--- got ---\n%s", got)
	}
}

func TestRenderSearchDefensiveLinks(t *testing.T) {
	tests := []struct {
		name   string
		title  string
		url    string
		wantIn string
	}{
		{"empty title falls back to url", "", "https://a.example/", "[https://a.example/](https://a.example/)"},
		{"empty url renders plain text", "Title only", "", "1. Title only"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderSearch(domain.SearchResult{Query: "x", Hits: []domain.SearchHit{hit(tt.title, "content", tt.url, nil, 0, "")}})
			if !strings.Contains(got, tt.wantIn) {
				t.Errorf("output missing %q\n--- got ---\n%s", tt.wantIn, got)
			}
		})
	}
}

func TestSnippet(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"empty", "", 300, ""},
		{"blank lines only", "\n   \n\t\n", 300, ""},
		{"first non-empty line", "first\nsecond", 300, "first"},
		{"leading blank lines", "\n\n  hello world  \nnext", 300, "hello world"},
		{"long truncated", strings.Repeat("a", 500), 300, strings.Repeat("a", 300) + "…"},
		{"max boundary", strings.Repeat("b", 300), 300, strings.Repeat("b", 300)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := snippet(tt.in, tt.max); got != tt.want {
				t.Errorf("snippet(%q, %d) = %q, want %q", tt.in, tt.max, got, tt.want)
			}
		})
	}
}

func TestJoinEngines(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"nil", nil, ""},
		{"empty", []string{}, ""},
		{"single", []string{"duckduckgo"}, "duckduckgo"},
		{"multiple", []string{"duckduckgo", "wikipedia", "brave"}, "duckduckgo, wikipedia, brave"},
		{"blank entries dropped", []string{" a ", "", "b"}, "a, b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := joinEngines(tt.in); got != tt.want {
				t.Errorf("joinEngines(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
