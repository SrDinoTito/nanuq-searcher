package result

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestMainResultAsDictSnakeCase pins the SearXNG JSON contract (REQ-018,
// CA-003): all keys present, exact snake_case names, zero/empty fields NOT
// omitted.
func TestMainResultAsDictSnakeCase(t *testing.T) {
	m := &MainResult{
		Title:     "Example",
		Content:   "Snippet",
		URL:       "https://example.org/page?q=1#frag",
		Thumbnail: "https://example.org/thumb.png",
		ImgSrc:    "https://example.org/img.png",
		Engines:   []string{"google", "bing"},
		Score:     1.5,
		Category:  "general",
		Positions: []int{1},
		Priority:  1,
		Template:  "default.html",
	}

	d := m.AsDict()

	// Every contract key must be present — empty ones too.
	wantKeys := []string{"title", "content", "url", "thumbnail", "img_src",
		"engines", "score", "category", "positions", "priority", "template",
		"parsed_url"}
	for _, k := range wantKeys {
		if _, ok := d[k]; !ok {
			t.Errorf("AsDict must contain key %q (empty fields are not omitted)", k)
		}
	}
	if len(d) != len(wantKeys) {
		t.Errorf("AsDict must produce exactly %d keys, got %d: %v", len(wantKeys), len(d), d)
	}

	if d["title"] != "Example" {
		t.Errorf("title: got %v", d["title"])
	}
	if d["content"] != "Snippet" {
		t.Errorf("content: got %v", d["content"])
	}
	if d["url"] != "https://example.org/page?q=1#frag" {
		t.Errorf("url: got %v", d["url"])
	}
	if d["thumbnail"] != "https://example.org/thumb.png" {
		t.Errorf("thumbnail: got %v", d["thumbnail"])
	}
	if d["img_src"] != "https://example.org/img.png" {
		t.Errorf("img_src: got %v", d["img_src"])
	}
	if !reflect.DeepEqual(d["engines"], []string{"google", "bing"}) {
		t.Errorf("engines: got %v", d["engines"])
	}
	if d["score"] != 1.5 {
		t.Errorf("score: got %v", d["score"])
	}
	if d["category"] != "general" {
		t.Errorf("category: got %v", d["category"])
	}
	if !reflect.DeepEqual(d["positions"], []int{1}) {
		t.Errorf("positions: got %v", d["positions"])
	}
	if d["priority"] != 1 {
		t.Errorf("priority: got %v", d["priority"])
	}
	if d["template"] != "default.html" {
		t.Errorf("template: got %v", d["template"])
	}

	// parsed_url mirrors urllib.ParseResult: [scheme, netloc, path, params,
	// query, fragment].
	wantParsed := []any{"https", "example.org", "/page", "", "q=1", "frag"}
	if !reflect.DeepEqual(d["parsed_url"], wantParsed) {
		t.Errorf("parsed_url: got %v, want %v", d["parsed_url"], wantParsed)
	}
}

// TestMainResultAsDictEmptyFieldsNotOmitted verifies zero-valued fields stay
// in the output, and parsed_url is nil for an empty URL (Python: None).
func TestMainResultAsDictEmptyFieldsNotOmitted(t *testing.T) {
	d := (&MainResult{URL: ""}).AsDict()

	for _, k := range []string{"title", "content", "url", "thumbnail",
		"img_src", "engines", "score", "category", "positions", "priority",
		"template", "parsed_url"} {
		if _, ok := d[k]; !ok {
			t.Errorf("key %q must be present even when empty", k)
		}
	}
	if d["parsed_url"] != nil {
		t.Errorf("parsed_url must be nil for empty URL, got %v", d["parsed_url"])
	}
	if d["template"] != "default.html" {
		t.Errorf("empty template must serialize as the SearXNG default: got %v", d["template"])
	}
	if d["score"] != 0.0 {
		t.Errorf("empty score must serialize as 0: got %v", d["score"])
	}
	if d["priority"] != 0 {
		t.Errorf("empty priority must serialize as 0: got %v", d["priority"])
	}
	if !reflect.DeepEqual(d["engines"], []string(nil)) {
		t.Errorf("empty engines must serialize as empty array: got %v", d["engines"])
	}
}

// TestMainResultAsDictJSONRoundTrip ensures the map is JSON-serializable with
// the exact snake_case keys the API exposes (DSG-014).
func TestMainResultAsDictJSONRoundTrip(t *testing.T) {
	m := &MainResult{
		Title:     "Example",
		Content:   "Snippet",
		URL:       "https://example.org/page",
		Engines:   []string{"google"},
		Score:     1.0,
		Category:  "general",
		Positions: []int{1},
	}

	raw, err := json.Marshal(m.AsDict())
	if err != nil {
		t.Fatalf("AsDict must be JSON-serializable: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("invalid JSON produced: %v", err)
	}
	for _, k := range []string{"title", "content", "url", "thumbnail", "img_src",
		"engines", "score", "category", "positions", "priority", "template",
		"parsed_url"} {
		if _, ok := got[k]; !ok {
			t.Errorf("JSON must contain key %q, got: %s", k, raw)
		}
	}
}

// TestAnswerAndInfoboxAsDict pins the answers/infoboxes snake_case contract.
func TestAnswerAndInfoboxAsDict(t *testing.T) {
	a := Answer{Title: "42", Content: "the answer"}
	ad := a.AsDict()
	if len(ad) != 2 || ad["title"] != "42" || ad["content"] != "the answer" {
		t.Errorf("Answer.AsDict: got %v", ad)
	}

	ib := Infobox{
		Title:      "Earth",
		Content:    "third planet",
		URLs:       []string{"https://example.org/earth"},
		Attributes: []KeyValue{{Key: "Radius", Value: "6371 km"}},
		ImgSrc:     "https://example.org/earth.png",
	}
	ibd := ib.AsDict()
	if ibd["title"] != "Earth" || ibd["content"] != "third planet" {
		t.Errorf("Infobox.AsDict title/content: got %v", ibd)
	}
	if !reflect.DeepEqual(ibd["urls"], []string{"https://example.org/earth"}) {
		t.Errorf("Infobox.AsDict urls: got %v", ibd["urls"])
	}
	wantAttrs := []map[string]any{{"key": "Radius", "value": "6371 km"}}
	if !reflect.DeepEqual(ibd["attributes"], wantAttrs) {
		t.Errorf("Infobox.AsDict attributes: got %v", ibd["attributes"])
	}
	if ibd["img_src"] != "https://example.org/earth.png" {
		t.Errorf("Infobox.AsDict img_src: got %v", ibd["img_src"])
	}
}

// TestMainResultAsDictEnginesJSON pins that a MainResult carrying engines
// emits the snake_case "engines" array in the JSON response — the field the
// container now populates in Extend (result['engine'] = engine_name,
// results.py L88), e.g. "engines":["brave"].
func TestMainResultAsDictEnginesJSON(t *testing.T) {
	raw, err := json.Marshal((&MainResult{
		Title:   "T",
		URL:     "https://x.example",
		Engines: []string{"brave"},
	}).AsDict())
	if err != nil {
		t.Fatalf("AsDict must be JSON-serializable: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("invalid JSON produced: %v", err)
	}
	if string(got["engines"]) != `["brave"]` {
		t.Errorf("engines JSON = %s, want [\"brave\"]", got["engines"])
	}
}
