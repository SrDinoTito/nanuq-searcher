package engines

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
	"nanuq-engine/internal/result"
)

// mustBraveEngine builds a Brave engine with the given overrides, failing
// the test on error.
func mustBraveEngine(t *testing.T, overrides map[string]any) engine.Engine {
	t.Helper()
	e, err := NewBraveEngine(&config.EngineConfig{
		Name:      "test",
		Engine:    "brave",
		Shortcut:  "b",
		Overrides: overrides,
	})
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}
	return e
}

// TestBraveRequestURLFormat checks the request URL, header and cookies
// (brave.py L197-231). Note: safe search goes into the safesearch cookie,
// not a query parameter as the task text suggested — the Python module is
// the source of truth.
func TestBraveRequestURLFormat(t *testing.T) {
	e := mustBraveEngine(t, nil)
	params := &engine.RequestParams{}
	if err := e.Request("test", params); err != nil {
		t.Fatalf("Request returned error: %v", err)
	}
	wantURL := "https://search.brave.com/search?q=test&source=web"
	if params.URL != wantURL {
		t.Errorf("URL = %q, want %q", params.URL, wantURL)
	}
	if params.Method != http.MethodGet {
		t.Errorf("Method = %q, want GET", params.Method)
	}
	if got := params.Headers.Get("Accept-Encoding"); got != "gzip, deflate" {
		t.Errorf("Accept-Encoding = %q, want gzip, deflate", got)
	}
	if got := cookieValue(params.Cookies, "safesearch"); got != "off" {
		t.Errorf("safesearch cookie = %q, want off", got)
	}
	if got := cookieValue(params.Cookies, "useLocation"); got != "0" {
		t.Errorf("useLocation cookie = %q, want 0", got)
	}
	if got := cookieValue(params.Cookies, "summarizer"); got != "0" {
		t.Errorf("summarizer cookie = %q, want 0", got)
	}
	if got := cookieValue(params.Cookies, "country"); got != "all" {
		t.Errorf("country cookie = %q, want all", got)
	}
	if got := cookieValue(params.Cookies, "ui_lang"); got != "en-us" {
		t.Errorf("ui_lang cookie = %q, want en-us", got)
	}
}

// TestBraveRequestPaging checks paging, time range and locale cookies on a
// later page (brave.py L206-210, L226-230).
func TestBraveRequestPaging(t *testing.T) {
	e := mustBraveEngine(t, nil)
	params := &engine.RequestParams{Pageno: 2, TimeRange: "week", SafeSearch: 2, Language: "en-US"}
	if err := e.Request("test", params); err != nil {
		t.Fatalf("Request returned error: %v", err)
	}
	wantURL := "https://search.brave.com/search?offset=1&q=test&source=web&tf=pw"
	if params.URL != wantURL {
		t.Errorf("URL = %q, want %q", params.URL, wantURL)
	}
	if got := cookieValue(params.Cookies, "safesearch"); got != "strict" {
		t.Errorf("safesearch cookie = %q, want strict", got)
	}
	if got := cookieValue(params.Cookies, "country"); got != "us" {
		t.Errorf("country cookie = %q, want us", got)
	}
	if got := cookieValue(params.Cookies, "ui_lang"); got != "en-us" {
		t.Errorf("ui_lang cookie = %q, want en-us", got)
	}
}

// TestBraveResponseParse checks response parsing: result extraction, the
// published-date lstrip, thumbnails, ad filtering and suggestions (brave.py
// L289-349).
func TestBraveResponseParse(t *testing.T) {
	body := `<!DOCTYPE html>
<html><body>
<div class="snippet ">
  <div class="snippet-title"><a href="https://example.org/page1">Result title</a></div>
  <div class="snippet-description content"><span class="t-secondary">2024-01-05</span> Actual snippet text here</div>
  <a class="thumbnail"><img src="https://example.org/thumb.png"/></a>
</div>
<div class="snippet ">
  <div class="snippet-title"><a href="/partial/url">Ad result</a></div>
</div>
<div class="snippet ">
  <a href="https://example.org/notitle">No title result</a>
</div>
<a class="related-query" href="#">related to test</a>
</body></html>`
	e := mustBraveEngine(t, nil)
	raw, err := e.Response(respBody(body))
	if err != nil {
		t.Fatalf("Response returned error: %v", err)
	}

	var mains []*result.MainResult
	var suggestions []string
	for _, rr := range raw {
		switch rr.Kind {
		case result.KindMain:
			mains = append(mains, rr.Main)
		case result.KindSuggestion:
			suggestions = append(suggestions, *rr.Str)
		}
	}

	if len(mains) != 1 {
		t.Fatalf("got %d main results, want 1 (ads and missing titles skipped)", len(mains))
	}
	if mains[0].URL != "https://example.org/page1" || mains[0].Title != "Result title" {
		t.Errorf("main[0] = %+v", mains[0])
	}
	if got := mains[0].Content; got != "Actual snippet text here" {
		t.Errorf("Content = %q, want %q (date prefix stripped)", got, "Actual snippet text here")
	}
	if got := mains[0].Thumbnail; got != "https://example.org/thumb.png" {
		t.Errorf("Thumbnail = %q, want %q", got, "https://example.org/thumb.png")
	}

	if len(suggestions) != 1 || suggestions[0] != "related to test" {
		t.Errorf("suggestions = %v, want [related to test]", suggestions)
	}
}

// TestBraveResponseUnsupportedCategory checks that non-search/goggles
// categories fail with a descriptive error (brave.py L266-286; the news,
// images and videos JS/JSON parsing is not ported).
func TestBraveResponseUnsupportedCategory(t *testing.T) {
	e := mustBraveEngine(t, map[string]any{"brave_category": "videos"})
	_, err := e.Response(respBody("<html></html>"))
	if err == nil {
		t.Fatal("expected error for unsupported brave category")
	}
	if !strings.Contains(err.Error(), "unsupported brave category") {
		t.Errorf("err = %v, want it to mention the unsupported category", err)
	}
}

// TestNewBraveEngineNilConfig checks the nil config guard.
func TestNewBraveEngineNilConfig(t *testing.T) {
	_, err := NewBraveEngine(nil)
	if !errors.Is(err, engine.ErrInvalidConfig) {
		t.Fatalf("err = %v, want %v", err, engine.ErrInvalidConfig)
	}
}
