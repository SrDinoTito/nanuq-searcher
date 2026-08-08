package engines

import (
	"errors"
	"net/http"
	"testing"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
	"nanuq-engine/internal/result"
)

// mustGoogleEngine builds a Google engine with the given overrides, failing
// the test on error.
func mustGoogleEngine(t *testing.T, overrides map[string]any) engine.Engine {
	t.Helper()
	e, err := NewGoogleEngine(&config.EngineConfig{
		Name:      "test",
		Engine:    "google",
		Shortcut:  "g",
		Overrides: overrides,
	})
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}
	return e
}

// cookieValue returns the value of the named request cookie, or "".
func cookieValue(cookies []*http.Cookie, name string) string {
	for _, c := range cookies {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

// TestGoogleRequestURLFormat checks the request URL, header and cookie built
// by Request (google.py L303-344). Note: Google uses a GET request even
// though the task text described POST form data — the Python module is the
// source of truth.
func TestGoogleRequestURLFormat(t *testing.T) {
	e := mustGoogleEngine(t, nil)
	params := &engine.RequestParams{Language: "en", TimeRange: "day", SafeSearch: 2, Pageno: 1}
	if err := e.Request("test", params); err != nil {
		t.Fatalf("Request returned error: %v", err)
	}
	wantURL := "https://www.google.com/search?cr=&filter=0&hl=en&ie=utf8&lr=lang_en&oe=utf8&q=test&safe=high&start=0&tbs=qdr%3Ad"
	if params.URL != wantURL {
		t.Errorf("URL = %q, want %q", params.URL, wantURL)
	}
	if params.Method != http.MethodGet {
		t.Errorf("Method = %q, want GET", params.Method)
	}
	if got := params.Headers.Get("Accept"); got != "*/*" {
		t.Errorf("Accept header = %q, want */*", got)
	}
	if got := cookieValue(params.Cookies, "CONSENT"); got != "YES+" {
		t.Errorf("CONSENT cookie = %q, want YES+", got)
	}
}

// TestGoogleRequestDefault checks the default locale handling: no language
// means hl=en and empty lr/cr, and no safe parameter (google.py L339-340
// only emits safe when safesearch is active).
func TestGoogleRequestDefault(t *testing.T) {
	e := mustGoogleEngine(t, nil)
	params := &engine.RequestParams{}
	if err := e.Request("test", params); err != nil {
		t.Fatalf("Request returned error: %v", err)
	}
	wantURL := "https://www.google.com/search?cr=&filter=0&hl=en&ie=utf8&lr=&oe=utf8&q=test&start=0"
	if params.URL != wantURL {
		t.Errorf("URL = %q, want %q", params.URL, wantURL)
	}
}

// TestGoogleResponseParse checks response parsing: direct links, google
// redirector decoding, snippet extraction with script removal, data:image
// thumbnails and suggestions (google.py L361-427).
func TestGoogleResponseParse(t *testing.T) {
	body := `<!DOCTYPE html>
<html><body>
<div class="result-container">
  <div>
    <a data-ved="ved1" href="https://example.org/direct">
      <div style="margin:0"><h3>Direct result</h3></div>
      <img src='data:image/png;base64,QUJD' id='dimg_1'/>
    </a>
  </div>
  <div class="ilUpNd H66NU aSRlid"><span>Snippet one</span><script>bad();</script></div>
</div>
<div class="result-container">
  <div>
    <a data-ved="ved2" href="/url?q=https%3A%2F%2Fexample.org%2Freal%3Fa%3D1%26b%3D2&sa=U&ved=2ahUKE">
      <div style="margin:0"><h3>Redirected result</h3></div>
    </a>
  </div>
  <div class="ilUpNd H66NU aSRlid"><span>Snippet two</span></div>
</div>
<a data-ved="ved3" class="no-draw" href="https://example.org/excluded">
  <div style="margin:0"><h3>Excluded by class</h3></div>
</a>
<a href="https://example.org/noved">
  <div style="margin:0"><h3>Excluded by data-ved</h3></div>
</a>
<div class="gGQDvd iIWm4b"><a href="#">related to test</a></div>
</body></html>`
	e := mustGoogleEngine(t, nil)
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

	if len(mains) != 2 {
		t.Fatalf("got %d main results, want 2", len(mains))
	}
	if mains[0].URL != "https://example.org/direct" || mains[0].Title != "Direct result" {
		t.Errorf("main[0] = %+v", mains[0])
	}
	if got := mains[0].Content; got != "Snippet one" {
		t.Errorf("main[0].Content = %q, want %q (script removed)", got, "Snippet one")
	}
	if got := mains[0].Thumbnail; got != "data:image/png;base64,QUJD" {
		t.Errorf("main[0].Thumbnail = %q, want data:image resolved from the image map", got)
	}
	if mains[1].URL != "https://example.org/real?a=1&b=2" || mains[1].Title != "Redirected result" {
		t.Errorf("main[1] = %+v", mains[1])
	}
	if got := mains[1].Content; got != "Snippet two" {
		t.Errorf("main[1].Content = %q, want %q", got, "Snippet two")
	}

	if len(suggestions) != 1 || suggestions[0] != "related to test" {
		t.Errorf("suggestions = %v, want [related to test]", suggestions)
	}
}

// TestNewGoogleEngineNilConfig checks the nil config guard.
func TestNewGoogleEngineNilConfig(t *testing.T) {
	_, err := NewGoogleEngine(nil)
	if !errors.Is(err, engine.ErrInvalidConfig) {
		t.Fatalf("err = %v, want %v", err, engine.ErrInvalidConfig)
	}
}
