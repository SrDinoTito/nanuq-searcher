package result

import (
	"reflect"
	"testing"
)

// TestMergeSameURLFusesEnginesAndPositions is the REQ-009 acceptance case:
// two results sharing a URL are fused into one — engines become the set
// union, positions are appended, the longer title/content win.
func TestMergeSameURLFusesEnginesAndPositions(t *testing.T) {
	a := &MainResult{
		Title:     "short title",
		Content:   "short content",
		URL:       "https://example.org/doc",
		Engines:   []string{"google"},
		Positions: []int{1},
		Score:     0.5,
	}
	b := &MainResult{
		Title:     "a much longer title that should win",
		Content:   "a much longer content snippet that should win",
		URL:       "https://example.org/doc",
		Engines:   []string{"bing", "google"},
		Positions: []int{3},
		Score:     2.0,
	}

	merged := Merge(a, b)

	if merged != a {
		t.Fatal("Merge must mutate and return the first argument (origin), mirroring SearXNG")
	}
	if merged.Title != b.Title {
		t.Errorf("longer title must win: got %q, want %q", merged.Title, b.Title)
	}
	if merged.Content != b.Content {
		t.Errorf("longer content must win: got %q, want %q", merged.Content, b.Content)
	}
	wantEngines := []string{"google", "bing"}
	if !reflect.DeepEqual(merged.Engines, wantEngines) {
		t.Errorf("engines must be set union (origin first): got %v, want %v", merged.Engines, wantEngines)
	}
	wantPositions := []int{1, 3}
	if !reflect.DeepEqual(merged.Positions, wantPositions) {
		t.Errorf("positions must be appended: got %v, want %v", merged.Positions, wantPositions)
	}
	if merged.Score != 2.0 {
		t.Errorf("higher score must win: got %v, want 2.0", merged.Score)
	}
}

// TestMergeFillsEmptyFieldsFromOther mirrors Python defaults_from: fields
// that are empty in the origin are filled from the other result.
func TestMergeFillsEmptyFieldsFromOther(t *testing.T) {
	a := &MainResult{
		Title: "title",
		URL:   "https://example.org/doc",
	}
	b := &MainResult{
		Title:     "title",
		Content:   "content from b",
		URL:       "https://example.org/doc",
		Thumbnail: "https://example.org/thumb.png",
		ImgSrc:    "https://example.org/img.png",
		Category:  "images",
		Template:  "images.html",
		Priority:  2,
	}

	merged := Merge(a, b)

	if merged.Content != "content from b" {
		t.Errorf("empty content must be filled from b: got %q", merged.Content)
	}
	if merged.Thumbnail != "https://example.org/thumb.png" {
		t.Errorf("empty thumbnail must be filled from b: got %q", merged.Thumbnail)
	}
	if merged.ImgSrc != "https://example.org/img.png" {
		t.Errorf("empty img_src must be filled from b: got %q", merged.ImgSrc)
	}
	if merged.Category != "images" {
		t.Errorf("empty category must be filled from b: got %q", merged.Category)
	}
	if merged.Template != "images.html" {
		t.Errorf("empty template must be filled from b: got %q", merged.Template)
	}
	if merged.Priority != 2 {
		t.Errorf("zero priority must be filled from b: got %d", merged.Priority)
	}
	// Non-empty origin fields must NOT be overwritten.
	if merged.Title != "title" {
		t.Errorf("non-empty title must be preserved: got %q", merged.Title)
	}
}

// TestMergeUpgradesHTTPToHTTPS mirrors the URL scheme upgrade of
// merge_two_main_results: when only the other result is secure, the merged
// result adopts its scheme.
func TestMergeUpgradesHTTPToHTTPS(t *testing.T) {
	a := &MainResult{URL: "http://example.org/doc", Title: "t"}
	b := &MainResult{URL: "https://example.org/doc", Title: "t"}

	merged := Merge(a, b)

	if merged.URL != "https://example.org/doc" {
		t.Errorf("http must be upgraded to https: got %q", merged.URL)
	}
}

// TestMergeDoesNotDowngradeHTTPS ensures an already-secure URL is untouched.
func TestMergeDoesNotDowngradeHTTPS(t *testing.T) {
	a := &MainResult{URL: "https://example.org/doc", Title: "t"}
	b := &MainResult{URL: "http://example.org/doc", Title: "t"}

	merged := Merge(a, b)

	if merged.URL != "https://example.org/doc" {
		t.Errorf("https must not be downgraded: got %q", merged.URL)
	}
}
