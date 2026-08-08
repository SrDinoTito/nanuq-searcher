package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	nanuq "nanuq-engine/pkg/nanuq"
)

// newSearchTestServer builds a nanuq.Service from a minimal temp settings
// file (empty catalog, fully offline) and a Server wired to it. The handler
// reads s.svc, so the real service must be passed to NewServer.
func newSearchTestServer(t *testing.T) *mcpserver.MCPServer {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.yml")
	if err := os.WriteFile(path, []byte("engines: []\n"), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	svc, err := nanuq.NewServiceFromFile(path, nil)
	if err != nil {
		t.Fatalf("NewServiceFromFile: %v", err)
	}
	return NewServer(svc, nil, discardLogger())
}

// callSearch invokes the nanuq_search handler with the given arguments.
func callSearch(t *testing.T, srv *mcpserver.MCPServer, args map[string]any) *mcpgo.CallToolResult {
	t.Helper()
	st := srv.GetTool(ToolSearch)
	if st == nil {
		t.Fatalf("tool %q not registered", ToolSearch)
	}
	result, err := st.Handler(context.Background(), mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: ToolSearch, Arguments: args},
	})
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if result == nil {
		t.Fatal("handler returned nil result")
	}
	return result
}

// searchText extracts the TextContent of a call result.
func searchText(t *testing.T, result *mcpgo.CallToolResult) string {
	t.Helper()
	if len(result.Content) != 1 {
		t.Fatalf("Content has %d items, want 1", len(result.Content))
	}
	text, ok := result.Content[0].(mcpgo.TextContent)
	if !ok {
		t.Fatalf("Content[0] is %T, want mcpgo.TextContent", result.Content[0])
	}
	return text.Text
}

func TestHandleSearchMaxResultsOutOfRangeIsError(t *testing.T) {
	srv := newSearchTestServer(t)
	for _, mr := range []float64{0, 999, -3, 51} {
		result := callSearch(t, srv, map[string]any{
			"query":       "hola",
			"max_results": mr,
		})
		if !result.IsError {
			t.Errorf("max_results=%v: want isError=true", mr)
		}
	}
}

func TestHandleSearchMaxResultsNonNumberIsError(t *testing.T) {
	srv := newSearchTestServer(t)
	result := callSearch(t, srv, map[string]any{
		"query":       "hola",
		"max_results": "diez",
	})
	if !result.IsError {
		t.Error("non-number max_results: want isError=true")
	}
}

func TestHandleSearchQueryNotStringIsError(t *testing.T) {
	srv := newSearchTestServer(t)
	for _, q := range []any{42, []string{"hola"}, map[string]any{}} {
		result := callSearch(t, srv, map[string]any{"query": q})
		if !result.IsError {
			t.Errorf("query=%v (%T): want isError=true", q, q)
		}
	}
}

func TestHandleSearchMissingQueryIsError(t *testing.T) {
	srv := newSearchTestServer(t)
	result := callSearch(t, srv, map[string]any{})
	if !result.IsError {
		t.Error("missing query: want isError=true")
	}
}

func TestHandleSearchCategoriesNonStringIsError(t *testing.T) {
	srv := newSearchTestServer(t)
	for _, cats := range []any{
		[]any{"general", 42},
		"general",
		[]any{map[string]any{}},
	} {
		result := callSearch(t, srv, map[string]any{
			"query":      "hola",
			"categories": cats,
		})
		if !result.IsError {
			t.Errorf("categories=%v (%T): want isError=true", cats, cats)
		}
	}
}

func TestHandleSearchEmptyQueryRendersMarkdownNoError(t *testing.T) {
	srv := newSearchTestServer(t)
	result := callSearch(t, srv, map[string]any{"query": ""})
	if result.IsError {
		t.Error("empty query: want isError=false")
	}
	text := searchText(t, result)
	if !strings.Contains(text, "Búsqueda vacía") {
		t.Errorf("text = %q, want to contain %q", text, "Búsqueda vacía")
	}
}

func TestHandleSearchNormalQueryEmptyCatalog(t *testing.T) {
	srv := newSearchTestServer(t)
	result := callSearch(t, srv, map[string]any{"query": "hola"})
	if result.IsError {
		t.Error("normal query: want isError=false")
	}
	text := searchText(t, result)
	if !strings.Contains(text, "Sin resultados") {
		t.Errorf("text = %q, want to contain %q", text, "Sin resultados")
	}
	if !strings.Contains(text, "hola") {
		t.Errorf("text = %q, want to contain query %q", text, "hola")
	}
}

func TestHandleSearchDefaultMaxResultsAbsent(t *testing.T) {
	// max_results absent must default to config.DefaultSearchMaxResults and
	// still succeed (REQ-002).
	srv := newSearchTestServer(t)
	result := callSearch(t, srv, map[string]any{"query": "hola"})
	if result.IsError {
		t.Error("absent max_results: want isError=false")
	}
}

func TestHandleSearchExternalBangRendersRedirect(t *testing.T) {
	srv := newSearchTestServer(t)
	result := callSearch(t, srv, map[string]any{"query": "!!ddg hola"})
	if result.IsError {
		t.Error("external bang query: want isError=false")
	}
	text := searchText(t, result)
	if !strings.Contains(text, "Bang externo") {
		t.Errorf("text = %q, want to contain %q", text, "Bang externo")
	}
}

func TestHandleSearchNilServiceIsError(t *testing.T) {
	srv := NewServer(nil, nil, discardLogger())
	result := callSearch(t, srv, map[string]any{"query": "hola"})
	if !result.IsError {
		t.Error("nil service: want isError=true")
	}
}
