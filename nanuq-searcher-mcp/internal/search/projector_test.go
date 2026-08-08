package search

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"nanuq-searcher-mcp/internal/domain"
)

// junkKeys are the SearXNG dict keys that MUST never reach the clean domain
// (REQ-003 / AC-001): parsed_url, template, priority, thumbnail, img_src,
// positions.
var junkKeys = []string{"parsed_url", "template", "priority", "thumbnail", "img_src", "positions"}

func TestProject(t *testing.T) {
	tests := []struct {
		name string
		raw  map[string]any
		want domain.SearchHit
	}{
		{
			name: "full valid dict",
			raw: map[string]any{
				"title":    "Go Programming Language",
				"content":  "Go is an open source programming language.",
				"url":      "https://go.dev/",
				"engines":  []any{"duckduckgo", "wikipedia"},
				"score":    0.9876,
				"category": "general",
			},
			want: domain.SearchHit{
				Title:    "Go Programming Language",
				Content:  "Go is an open source programming language.",
				URL:      "https://go.dev/",
				Engines:  []string{"duckduckgo", "wikipedia"},
				Score:    0.9876,
				Category: "general",
			},
		},
		{
			name: "missing score",
			raw: map[string]any{
				"title":   "no score here",
				"content": "snippet",
				"url":     "https://example.com/",
				"engines": []string{"brave"},
			},
			want: domain.SearchHit{
				Title:   "no score here",
				Content: "snippet",
				URL:     "https://example.com/",
				Engines: []string{"brave"},
			},
		},
		{
			name: "engines as []any with mixed types",
			raw: map[string]any{
				"title":   "mixed engines",
				"engines": []any{"duckduckgo", 42, nil, "wikipedia", true},
			},
			want: domain.SearchHit{
				Title:   "mixed engines",
				Engines: []string{"duckduckgo", "wikipedia"},
			},
		},
		{
			name: "engines as []string directly",
			raw: map[string]any{
				"engines": []string{"google"},
			},
			want: domain.SearchHit{Engines: []string{"google"}},
		},
		{
			name: "score as int",
			raw: map[string]any{
				"score": 7,
			},
			want: domain.SearchHit{Score: 7},
		},
		{
			name: "score as int64",
			raw: map[string]any{
				"score": int64(9223372036854775807),
			},
			want: domain.SearchHit{Score: 9.223372036854776e+18},
		},
		{
			name: "score unexpected type",
			raw: map[string]any{
				"score": "0.9", // string, not numeric
			},
			want: domain.SearchHit{},
		},
		{
			name: "wrong types everywhere",
			raw: map[string]any{
				"title":    42,
				"content":  []int{1, 2},
				"url":      nil,
				"engines":  "not-a-slice",
				"score":    map[string]any{},
				"category": true,
			},
			want: domain.SearchHit{},
		},
		{
			name: "empty dict",
			raw:  map[string]any{},
			want: domain.SearchHit{},
		},
		{
			name: "nil dict",
			raw:  nil,
			want: domain.SearchHit{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Project(tt.raw)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Project(%q) = %+v, want %+v", tt.name, got, tt.want)
			}
		})
	}
}

// TestProjectCleanOutput guards REQ-003 / AC-001: a dict carrying every junk
// key must never leak any of them into the clean SearchHit.
func TestProjectCleanOutput(t *testing.T) {
	raw := map[string]any{
		// clean keys
		"title":    "Clean Title",
		"content":  "Clean content snippet.",
		"url":      "https://clean.example/",
		"engines":  []string{"duckduckgo"},
		"score":    0.5,
		"category": "general",
		// junk keys — sentinel values that must not appear anywhere
		"parsed_url": []any{"https", "clean.example", "/", "", "", ""},
		"template":   "junk-template.html",
		"priority":   99,
		"thumbnail":  "https://junk.example/thumb.png",
		"img_src":    "https://junk.example/img.png",
		"positions":  []int{1, 2, 3},
	}

	hit := Project(raw)

	// 1. Junk sentinels must not leak into any clean field.
	if strings.Contains(hit.Title, "junk") || strings.Contains(hit.Content, "junk") ||
		strings.Contains(hit.URL, "junk") || strings.Contains(hit.Category, "junk") {
		t.Fatalf("junk values leaked into clean fields: %+v", hit)
	}
	if len(hit.Engines) != 1 || hit.Engines[0] != "duckduckgo" {
		t.Errorf("engines polluted: %v", hit.Engines)
	}
	if hit.Score != 0.5 {
		t.Errorf("score polluted: %v", hit.Score)
	}

	// 2. The clean fields still carry the correct clean values.
	if hit.Title != "Clean Title" || hit.Content != "Clean content snippet." ||
		hit.URL != "https://clean.example/" || hit.Category != "general" {
		t.Errorf("clean fields corrupted: %+v", hit)
	}

	// 3. domain.SearchHit must have no field for any junk key (REQ-003).
	jt := reflect.TypeOf(domain.SearchHit{})
	for i := 0; i < jt.NumField(); i++ {
		name := jt.Field(i).Name
		for _, junk := range junkKeys {
			if strings.EqualFold(name, junk) {
				t.Errorf("SearchHit must not carry junk field %q", name)
			}
		}
	}

	// 4. JSON serialization must expose exactly the six clean keys.
	data, err := json.Marshal(hit)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	clean := []string{"title", "content", "url", "engines", "score", "category"}
	if len(got) != len(clean) {
		t.Fatalf("json keys = %v, want exactly %v", sortedKeys(got), clean)
	}
	for _, k := range clean {
		if _, ok := got[k]; !ok {
			t.Errorf("missing clean json key %q in %v", k, got)
		}
	}
	for _, junk := range junkKeys {
		if _, ok := got[junk]; ok {
			t.Errorf("junk json key %q present in clean output: %v", junk, got)
		}
	}
}

func TestProjectMany(t *testing.T) {
	raws := []map[string]any{
		{"title": "first", "url": "https://a.example/", "score": 1.0},
		{"title": "second", "url": "https://b.example/", "score": 2.0},
	}
	got := ProjectMany(raws)
	if len(got) != 2 {
		t.Fatalf("ProjectMany len = %d, want 2", len(got))
	}
	if got[0].Title != "first" || got[0].URL != "https://a.example/" || got[0].Score != 1.0 {
		t.Errorf("hit[0] = %+v", got[0])
	}
	if got[1].Title != "second" || got[1].Score != 2.0 {
		t.Errorf("hit[1] = %+v", got[1])
	}

	// nil input yields nil, never a panic.
	if got := ProjectMany(nil); got != nil {
		t.Errorf("ProjectMany(nil) = %v, want nil", got)
	}

	// empty input yields an empty (non-nil) slice.
	if got := ProjectMany([]map[string]any{}); got == nil || len(got) != 0 {
		t.Errorf("ProjectMany(empty) = %v, want empty slice", got)
	}
}

// sortedKeys returns the map keys in sorted order (stable test output).
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}
