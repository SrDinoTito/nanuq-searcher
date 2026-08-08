package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"nanuq-searcher-mcp/internal/config"
)

// fetchArticleBody is long enough to satisfy the readability char threshold so
// the parser treats the page as a proper article (mirrors extract_test.go).
const fetchArticleBody = "This is the body of a test article. It contains enough text to satisfy the readability char threshold so that the parser treats the page as a proper article and returns a non-nil node. Keep it over one hundred characters for safety."

const fetchArticleHTML = `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><title>Test Article</title></head><body><article><h1>Test Article</h1><p>` + fetchArticleBody + `</p></article></body></html>`

// fetchFullHTML carries content outside <article> (nav, footer) so mode=full
// can be distinguished from readability extraction.
const fetchFullHTML = `<!DOCTYPE html><html lang="en"><head><title>Full Page</title></head><body><nav><a href="/x">Nav link</a></nav><main><p>` + fetchArticleBody + `</p></main><footer>Footer note</footer></body></html>`

// fetchDefaultConfig returns the code-first MCP config used by the fetch tests.
func fetchDefaultConfig() *config.Config {
	c := config.Default()
	return &c
}

// newFetchTestServer builds an MCP server with the given cfg; svc is nil
// because the fetch handler never touches the search service.
func newFetchTestServer(cfg *config.Config) *mcpserver.MCPServer {
	return NewServer(nil, cfg, discardLogger())
}

// callFetch invokes the nanuq_fetch tool handler directly.
func callFetch(t *testing.T, srv *mcpserver.MCPServer, args map[string]any) *mcpgo.CallToolResult {
	t.Helper()
	st := srv.GetTool(ToolFetch)
	if st == nil {
		t.Fatalf("tool %q not registered", ToolFetch)
	}
	result, err := st.Handler(context.Background(), mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: ToolFetch, Arguments: args},
	})
	if err != nil {
		t.Fatalf("tool %q returned error: %v", ToolFetch, err)
	}
	if result == nil {
		t.Fatal("tool returned nil result")
	}
	return result
}

func TestHandleFetchReadableArticle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, fetchArticleHTML)
	}))
	defer srv.Close()

	result := callFetch(t, newFetchTestServer(fetchDefaultConfig()), map[string]any{
		"url": srv.URL,
	})
	if result.IsError {
		t.Fatalf("result has isError=true, text=%v", searchText(t, result))
	}
	text := searchText(t, result)
	for _, want := range []string{
		"# Test Article",
		"URL: " + srv.URL,
		fetchArticleBody,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q\noutput:\n%s", want, text)
		}
	}
}

func TestHandleFetchModeFullKeepsWholePage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, fetchFullHTML)
	}))
	defer srv.Close()

	result := callFetch(t, newFetchTestServer(fetchDefaultConfig()), map[string]any{
		"url":  srv.URL,
		"mode": "full",
	})
	if result.IsError {
		t.Fatalf("result has isError=true, text=%v", searchText(t, result))
	}
	text := searchText(t, result)
	for _, want := range []string{
		"# " + srv.URL,
		"URL: " + srv.URL,
		"Nav link",
		"Footer note",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q\noutput:\n%s", want, text)
		}
	}
}

func TestHandleFetchNotHTMLErrorRendersMarkdown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = fmt.Fprint(w, "just plain text")
	}))
	defer srv.Close()

	result := callFetch(t, newFetchTestServer(fetchDefaultConfig()), map[string]any{
		"url": srv.URL,
	})
	if result.IsError {
		t.Fatalf("domain error must not set isError")
	}
	text := searchText(t, result)
	for _, want := range []string{
		"## ⚠️ Error al obtener la página",
		"response is not HTML",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q\noutput:\n%s", want, text)
		}
	}
}

func TestHandleFetchHTTPErrorRendersMarkdown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	result := callFetch(t, newFetchTestServer(fetchDefaultConfig()), map[string]any{
		"url": srv.URL,
	})
	if result.IsError {
		t.Fatalf("domain error must not set isError")
	}
	text := searchText(t, result)
	for _, want := range []string{
		"## ⚠️ Error al obtener la página",
		"500",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q\noutput:\n%s", want, text)
		}
	}
}

func TestHandleFetchInvalidArgumentsIsError(t *testing.T) {
	srv := newFetchTestServer(fetchDefaultConfig())
	cases := []struct {
		name string
		args map[string]any
	}{
		{"missing url", map[string]any{}},
		{"empty url", map[string]any{"url": ""}},
		{"blank url", map[string]any{"url": "   "}},
		{"non-string url", map[string]any{"url": 42}},
		{"invalid mode", map[string]any{"url": "http://example.com", "mode": "summary"}},
		{"non-string mode", map[string]any{"url": "http://example.com", "mode": 7}},
		{"max_bytes below range", map[string]any{"url": "http://example.com", "max_bytes": float64(1000)}},
		{"max_bytes above range", map[string]any{"url": "http://example.com", "max_bytes": float64(20 << 20)}},
		{"non-number max_bytes", map[string]any{"url": "http://example.com", "max_bytes": "mucho"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := callFetch(t, srv, tc.args)
			if !result.IsError {
				t.Errorf("want isError=true, got text=%v", searchText(t, result))
			}
		})
	}
}

func TestHandleFetchCharsetISO885915Decoded(t *testing.T) {
	// Raw byte 0xE9 is 'é' in ISO-8859-15; the meta prescan must surface the
	// charset so ConvertHTML decodes it (proven by fetch client_test.go).
	body := "<html><head><meta charset=\"iso-8859-15\"><title>caf\xe9</title></head><body><p>caf\xe9</p></body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, body)
	}))
	defer srv.Close()

	result := callFetch(t, newFetchTestServer(fetchDefaultConfig()), map[string]any{
		"url":  srv.URL,
		"mode": "full",
	})
	if result.IsError {
		t.Fatalf("result has isError=true, text=%v", searchText(t, result))
	}
	text := searchText(t, result)
	if !strings.Contains(text, "café") {
		t.Errorf("output does not contain decoded \"café\"\noutput:\n%s", text)
	}
}

func TestHandleFetchTruncationNote(t *testing.T) {
	big := strings.Repeat("lorem ipsum dolor sit amet consectetur adipiscing elit ", 4000)
	body := "<html><head><title>Big</title></head><body><p>" + big + "</p></body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, body)
	}))
	defer srv.Close()

	result := callFetch(t, newFetchTestServer(fetchDefaultConfig()), map[string]any{
		"url":       srv.URL,
		"mode":      "full",
		"max_bytes": float64(64 << 10),
	})
	if result.IsError {
		t.Fatalf("result has isError=true, text=%v", searchText(t, result))
	}
	text := searchText(t, result)
	if !strings.Contains(text, "_[truncado") {
		t.Errorf("output missing truncation note\noutput:\n%s", text)
	}
}

func TestHandleFetchRedirectUsesFinalURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/target", http.StatusFound)
	})
	mux.HandleFunc("/target", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, fetchArticleHTML)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	result := callFetch(t, newFetchTestServer(fetchDefaultConfig()), map[string]any{
		"url": srv.URL + "/start",
	})
	if result.IsError {
		t.Fatalf("result has isError=true, text=%v", searchText(t, result))
	}
	text := searchText(t, result)
	for _, want := range []string{
		"# Test Article",
		"URL: " + srv.URL + "/target",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q\noutput:\n%s", want, text)
		}
	}
}

func TestHandleFetchNilConfigUsesFetchDefaults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, fetchArticleHTML)
	}))
	defer srv.Close()

	// cfg=nil exercises the fetchClientFromConfig fallback branch (DSG-010).
	result := callFetch(t, newFetchTestServer(nil), map[string]any{
		"url": srv.URL,
	})
	if result.IsError {
		t.Fatalf("result has isError=true, text=%v", searchText(t, result))
	}
	text := searchText(t, result)
	for _, want := range []string{"# Test Article", "URL: " + srv.URL} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q\noutput:\n%s", want, text)
		}
	}
}

func TestHandleFetchReadableFallbackToFull(t *testing.T) {
	// Readability yields OK=false for a bodyless page, so the handler falls
	// back to the full document (REQ-008) and the title to the final URL.
	body := "<!DOCTYPE html><html lang=\"en\"><head><title>Empty</title></head><body></body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, body)
	}))
	defer srv.Close()

	result := callFetch(t, newFetchTestServer(fetchDefaultConfig()), map[string]any{
		"url": srv.URL,
	})
	if result.IsError {
		t.Fatalf("result has isError=true, text=%v", searchText(t, result))
	}
	text := searchText(t, result)
	for _, want := range []string{
		"# " + srv.URL,
		"URL: " + srv.URL,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q\noutput:\n%s", want, text)
		}
	}
}

func TestHandleFetchUnsupportedScheme(t *testing.T) {
	result := callFetch(t, newFetchTestServer(fetchDefaultConfig()), map[string]any{
		"url": "ftp://example.com/x",
	})
	if result.IsError {
		t.Fatalf("domain error must not set isError")
	}
	text := searchText(t, result)
	for _, want := range []string{
		"## ⚠️ Error al obtener la página",
		"unsupported URL scheme",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q\noutput:\n%s", want, text)
		}
	}
}
