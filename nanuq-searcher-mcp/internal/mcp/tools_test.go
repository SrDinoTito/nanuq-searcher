package mcp

import (
	"reflect"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"nanuq-searcher-mcp/internal/config"
)

// prop returns the property schema map for name, failing the test if absent.
func prop(t *testing.T, tool mcpgo.Tool, name string) map[string]any {
	t.Helper()
	p, ok := tool.InputSchema.Properties[name]
	if !ok {
		t.Fatalf("tool %q: missing property %q", tool.Name, name)
	}
	m, ok := p.(map[string]any)
	if !ok {
		t.Fatalf("tool %q: property %q has type %T, want map[string]any", tool.Name, name, p)
	}
	return m
}

// wantNum asserts that prop[key] holds the integer want.
func wantNum(t *testing.T, tool mcpgo.Tool, property, key string, want int) {
	t.Helper()
	got, ok := prop(t, tool, property)[key]
	if !ok {
		t.Fatalf("tool %q: property %q missing %q", tool.Name, property, key)
	}
	if got != want {
		t.Errorf("tool %q: property %q.%s = %v, want %d", tool.Name, property, key, got, want)
	}
}

func TestSearchToolSchema(t *testing.T) {
	tool := searchTool()
	if tool.Name != ToolSearch {
		t.Errorf("name = %q, want %q", tool.Name, ToolSearch)
	}
	if tool.Description == "" {
		t.Error("description is empty")
	}
	if len(tool.InputSchema.Properties) != 3 {
		t.Errorf("properties = %d, want 3", len(tool.InputSchema.Properties))
	}
	if got := tool.InputSchema.Required; len(got) != 1 || got[0] != "query" {
		t.Errorf("required = %v, want [query]", got)
	}

	if got := prop(t, tool, "query")["type"]; got != "string" {
		t.Errorf("query.type = %v, want string", got)
	}

	cats := prop(t, tool, "categories")
	if got := cats["type"]; got != "array" {
		t.Errorf("categories.type = %v, want array", got)
	}
	if want := map[string]any{"type": "string"}; !reflect.DeepEqual(cats["items"], want) {
		t.Errorf("categories.items = %v, want %v", cats["items"], want)
	}

	if got := prop(t, tool, "max_results")["type"]; got != "integer" {
		t.Errorf("max_results.type = %v, want integer", got)
	}
	wantNum(t, tool, "max_results", "default", config.DefaultSearchMaxResults)
	wantNum(t, tool, "max_results", "minimum", 1)
	wantNum(t, tool, "max_results", "maximum", 50)
}

func TestFetchToolSchema(t *testing.T) {
	tool := fetchTool()
	if tool.Name != ToolFetch {
		t.Errorf("name = %q, want %q", tool.Name, ToolFetch)
	}
	if tool.Description == "" {
		t.Error("description is empty")
	}
	if len(tool.InputSchema.Properties) != 3 {
		t.Errorf("properties = %d, want 3", len(tool.InputSchema.Properties))
	}
	if got := tool.InputSchema.Required; len(got) != 1 || got[0] != "url" {
		t.Errorf("required = %v, want [url]", got)
	}

	if got := prop(t, tool, "url")["type"]; got != "string" {
		t.Errorf("url.type = %v, want string", got)
	}

	mode := prop(t, tool, "mode")
	if got := mode["type"]; got != "string" {
		t.Errorf("mode.type = %v, want string", got)
	}
	if got := mode["default"]; got != "readable" {
		t.Errorf("mode.default = %v, want readable", got)
	}
	if want := []string{"readable", "full"}; !reflect.DeepEqual(mode["enum"], want) {
		t.Errorf("mode.enum = %v, want %v", mode["enum"], want)
	}

	if got := prop(t, tool, "max_bytes")["type"]; got != "integer" {
		t.Errorf("max_bytes.type = %v, want integer", got)
	}
	wantNum(t, tool, "max_bytes", "default", config.DefaultFetchMaxBytes)
	wantNum(t, tool, "max_bytes", "minimum", 64<<10)
	wantNum(t, tool, "max_bytes", "maximum", 10<<20)
}

func TestMapToolSchema(t *testing.T) {
	tool := mapTool()
	if tool.Name != ToolMap {
		t.Errorf("name = %q, want %q", tool.Name, ToolMap)
	}
	if tool.Description == "" {
		t.Error("description is empty")
	}
	if len(tool.InputSchema.Properties) != 6 {
		t.Errorf("properties = %d, want 6", len(tool.InputSchema.Properties))
	}
	if got := tool.InputSchema.Required; len(got) != 1 || got[0] != "url" {
		t.Errorf("required = %v, want [url]", got)
	}

	if got := prop(t, tool, "url")["type"]; got != "string" {
		t.Errorf("url.type = %v, want string", got)
	}

	wantNum(t, tool, "max_pages", "default", config.DefaultCrawlMaxPages)
	wantNum(t, tool, "max_depth", "default", config.DefaultCrawlMaxDepth)

	for name, want := range map[string]bool{
		"same_host":       true,
		"respect_robots":  true,
		"include_content": false,
	} {
		p := prop(t, tool, name)
		if got := p["type"]; got != "boolean" {
			t.Errorf("%s.type = %v, want boolean", name, got)
		}
		if got := p["default"]; got != want {
			t.Errorf("%s.default = %v, want %t", name, got, want)
		}
	}
}
