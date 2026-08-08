package domain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestSearchResultConstruction is a smoke test for the clean search domain
// types (DSG-004): SearchResult with Hits and Unresponsive.
func TestSearchResultConstruction(t *testing.T) {
	res := SearchResult{
		Query: "golang",
		Hits: []SearchHit{
			{
				Title:    "The Go Programming Language",
				Content:  "Go is an open source programming language.",
				URL:      "https://go.dev/",
				Engines:  []string{"duckduckgo", "wikipedia"},
				Score:    0.98,
				Category: "general",
			},
		},
		Unresponsive: []string{"brave", "bing"},
		RedirectURL:  "https://google.com/search?q=golang",
	}

	if res.Query != "golang" {
		t.Errorf("Query = %q, want %q", res.Query, "golang")
	}
	if len(res.Hits) != 1 {
		t.Fatalf("len(Hits) = %d, want 1", len(res.Hits))
	}
	hit := res.Hits[0]
	if hit.Title != "The Go Programming Language" ||
		hit.Content != "Go is an open source programming language." ||
		hit.URL != "https://go.dev/" ||
		!reflect.DeepEqual(hit.Engines, []string{"duckduckgo", "wikipedia"}) ||
		hit.Score != 0.98 ||
		hit.Category != "general" {
		t.Errorf("Hit = %+v", hit)
	}
	if !reflect.DeepEqual(res.Unresponsive, []string{"brave", "bing"}) {
		t.Errorf("Unresponsive = %v", res.Unresponsive)
	}
	if res.RedirectURL != "https://google.com/search?q=golang" {
		t.Errorf("RedirectURL = %q", res.RedirectURL)
	}
}

// TestSearchResultJSONRoundTrip checks that the clean types serialize with
// the snake_case JSON contract and that optional fields respect omitempty.
func TestSearchResultJSONRoundTrip(t *testing.T) {
	res := SearchResult{
		Query: "golang",
		Hits: []SearchHit{
			{Title: "Go", URL: "https://go.dev/", Engines: []string{"ddg"}, Score: 0.5},
		},
	}
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	got := string(data)

	for _, want := range []string{`"query":"golang"`, `"hits":`, `"title":"Go"`, `"engines"`, `"score":0.5`} {
		if !strings.Contains(got, want) {
			t.Errorf("marshal output missing %q: %s", want, got)
		}
	}
	// Optional fields must be omitted when empty (omitempty).
	for _, absent := range []string{`"unresponsive"`, `"redirect_url"`} {
		if strings.Contains(got, absent) {
			t.Errorf("marshal output should omit %q: %s", absent, got)
		}
	}

	// Round-trip must preserve values.
	var back SearchResult
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(back, res) {
		t.Errorf("round-trip mismatch:\n got  %+v\n want %+v", back, res)
	}
}

// TestPageAndSiteMapSmoke exercises the crawl-side domain types (DSG-004).
func TestPageAndSiteMapSmoke(t *testing.T) {
	sm := SiteMap{
		RootURL: "https://example.com/",
		Pages: []Page{
			{
				URL:   "https://example.com/",
				Title: "Inicio",
				Depth: 0,
				Headings: []Heading{
					{Level: 1, Text: "Bienvenido"},
					{Level: 2, Text: "Guía"},
				},
				Content: "# Bienvenido",
			},
		},
		Visited:    1,
		HostErrors: map[string]string{"example.com": "429"},
	}

	if sm.RootURL != "https://example.com/" || sm.Visited != 1 || len(sm.Pages) != 1 {
		t.Fatalf("SiteMap = %+v", sm)
	}
	if sm.Cancelled {
		t.Errorf("Cancelled should default to false")
	}
	p := sm.Pages[0]
	if p.URL != "https://example.com/" || p.Title != "Inicio" || p.Depth != 0 {
		t.Errorf("Page = %+v", p)
	}
	if len(p.Headings) != 2 || p.Headings[0].Level != 1 || p.Headings[1].Text != "Guía" {
		t.Errorf("Headings = %+v", p.Headings)
	}
	if err := sm.HostErrors["example.com"]; err != "429" {
		t.Errorf("HostErrors = %v", sm.HostErrors)
	}
}
