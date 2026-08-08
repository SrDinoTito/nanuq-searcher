package engines

import (
	"errors"
	"net/http"
	"testing"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
	"nanuq-engine/internal/result"
)

// mustMojeekEngine builds a Mojeek engine for tests, failing on constructor
// error.
func mustMojeekEngine(t *testing.T, cfg *config.EngineConfig) engine.Engine {
	t.Helper()
	e, err := NewMojeekEngine(cfg)
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}
	return e
}

// TestMojeekRequestURLFormat checks the request URL for page 1 (no offset —
// s=0 triggers a rate limit, so the .py only sends s when pageno > 1) and for
// a later page (s = 10 * (pageno - 1), mojeek.py L96-102).
func TestMojeekRequestURLFormat(t *testing.T) {
	e := mustMojeekEngine(t, &config.EngineConfig{Name: "test", Engine: "mojeek", Shortcut: "mj"})

	// Page 1, safesearch on: args = {q: "test", safe: 1}; no "s" parameter.
	params := &engine.RequestParams{SafeSearch: 1, Pageno: 1}
	if err := e.Request("test", params); err != nil {
		t.Fatalf("Request returned error: %v", err)
	}
	wantURL := "https://www.mojeek.com/search?q=test&safe=1"
	if params.URL != wantURL {
		t.Errorf("URL = %q, want %q", params.URL, wantURL)
	}
	if params.Method != http.MethodGet {
		t.Errorf("Method = %q, want GET", params.Method)
	}

	// Page 3, safesearch off: args = {q: "test", safe: 0, s: 20}.
	params = &engine.RequestParams{SafeSearch: 0, Pageno: 3}
	if err := e.Request("test", params); err != nil {
		t.Fatalf("Request returned error: %v", err)
	}
	wantURL = "https://www.mojeek.com/search?q=test&s=20&safe=0"
	if params.URL != wantURL {
		t.Errorf("URL = %q, want %q", params.URL, wantURL)
	}
}

// TestMojeekRequestSafeClamp checks that safesearch is clamped to at most 1
// (mojeek.py L97: min(params["safesearch"], 1)).
func TestMojeekRequestSafeClamp(t *testing.T) {
	e := mustMojeekEngine(t, &config.EngineConfig{Name: "test", Engine: "mojeek", Shortcut: "mj"})
	params := &engine.RequestParams{SafeSearch: 2, Pageno: 1}
	if err := e.Request("test", params); err != nil {
		t.Fatalf("Request returned error: %v", err)
	}
	wantURL := "https://www.mojeek.com/search?q=test&safe=1"
	if params.URL != wantURL {
		t.Errorf("URL = %q, want %q", params.URL, wantURL)
	}
}

// TestMojeekResponseParse checks response parsing: result extraction via the
// .py XPaths (results_xpath / url_xpath / title_xpath / content_xpath) and
// spelling suggestions (suggestion_xpath, mojeek.py L126-139).
func TestMojeekResponseParse(t *testing.T) {
	body := `<!DOCTYPE html>
<html>
<head></head>
<body>
<div class="top-info">
  <p class="top-info spell">Did you mean <em><a href="/search?q=better">better</a></em></p>
</div>
<ul class="results-standard">
  <li>
    <a class="ob" href="https://example.org/one">One</a>
    <h2><a href="https://example.org/one">Result One</a></h2>
    <p class="s">first snippet</p>
  </li>
  <li>
    <a class="ob" href="https://example.org/two">Two</a>
    <h2><a href="https://example.org/two">Result Two</a></h2>
    <p class="s">second snippet</p>
  </li>
  <li>
    <a class="ob" href="">Broken</a>
    <h2><a href="">No href</a></h2>
  </li>
</ul>
</body>
</html>`
	e := mustMojeekEngine(t, &config.EngineConfig{Name: "test", Engine: "mojeek", Shortcut: "mj"})
	got, err := e.Response(respBody(body))
	if err != nil {
		t.Fatalf("Response returned error: %v", err)
	}
	// 2 main results + 1 spelling suggestion; the item with an empty href is
	// skipped.
	if len(got) != 3 {
		t.Fatalf("got %d results, want 3", len(got))
	}
	if got[0].Kind != result.KindMain {
		t.Fatalf("result 0 Kind = %v, want KindMain", got[0].Kind)
	}
	if got[0].Main.URL != "https://example.org/one" {
		t.Errorf("result 0 URL = %q", got[0].Main.URL)
	}
	if got[0].Main.Title != "Result One" {
		t.Errorf("result 0 Title = %q", got[0].Main.Title)
	}
	if got[0].Main.Content != "first snippet" {
		t.Errorf("result 0 Content = %q, want %q", got[0].Main.Content, "first snippet")
	}
	if got[1].Main.URL != "https://example.org/two" {
		t.Errorf("result 1 URL = %q", got[1].Main.URL)
	}
	if got[1].Main.Title != "Result Two" {
		t.Errorf("result 1 Title = %q", got[1].Main.Title)
	}
	if got[1].Main.Content != "second snippet" {
		t.Errorf("result 1 Content = %q, want %q", got[1].Main.Content, "second snippet")
	}
	if got[2].Kind != result.KindSuggestion {
		t.Fatalf("result 2 Kind = %v, want KindSuggestion", got[2].Kind)
	}
	if got[2].Str == nil || *got[2].Str != "better" {
		t.Errorf("result 2 suggestion = %v, want %q", got[2].Str, "better")
	}
}

// TestNewMojeekEngineNilConfig checks the constructor's nil-config guard.
func TestNewMojeekEngineNilConfig(t *testing.T) {
	if _, err := NewMojeekEngine(nil); !errors.Is(err, engine.ErrInvalidConfig) {
		t.Errorf("NewMojeekEngine(nil) error = %v, want ErrInvalidConfig", err)
	}
}
