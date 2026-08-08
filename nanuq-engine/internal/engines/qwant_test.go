package engines

import (
	"errors"
	"net/http"
	"testing"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
	"nanuq-engine/internal/result"
)

// mustQwantEngine builds a Qwant engine for tests, failing on constructor
// error.
func mustQwantEngine(t *testing.T, cfg *config.EngineConfig) engine.Engine {
	t.Helper()
	e, err := NewQwantEngine(cfg)
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}
	return e
}

// TestQwantRequestURLFormat checks the request contract: POST to the API URL
// with Content-Type application/json (spec deviation from the .py GET — see
// NewQwantEngine).
func TestQwantRequestURLFormat(t *testing.T) {
	e := mustQwantEngine(t, &config.EngineConfig{Name: "test", Engine: "qwant", Shortcut: "qw"})
	params := &engine.RequestParams{Language: "fr-FR", SafeSearch: 1}
	if err := e.Request("test", params); err != nil {
		t.Fatalf("Request returned error: %v", err)
	}
	if want := "https://api.qwant.com/api/search/web"; params.URL != want {
		t.Errorf("URL = %q, want %q", params.URL, want)
	}
	if params.Method != http.MethodPost {
		t.Errorf("Method = %q, want POST", params.Method)
	}
	if got := params.Headers.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json")
	}
}

// TestQwantRequestBody checks the JSON body fields built by Request (spec
// deviation from the .py GET args — see NewQwantEngine).
func TestQwantRequestBody(t *testing.T) {
	e := mustQwantEngine(t, &config.EngineConfig{Name: "test", Engine: "qwant", Shortcut: "qw"})
	params := &engine.RequestParams{Language: "fr-FR", SafeSearch: 1, Pageno: 3}
	if err := e.Request("test", params); err != nil {
		t.Fatalf("Request returned error: %v", err)
	}
	body, ok := params.JSON.(map[string]any)
	if !ok {
		t.Fatalf("JSON = %T, want map[string]any", params.JSON)
	}
	if got := body["q"]; got != "test" {
		t.Errorf("q = %v, want %q", got, "test")
	}
	if got := body["locale"]; got != "fr-FR" {
		t.Errorf("locale = %v, want %q", got, "fr-FR")
	}
	if got := body["offset"]; got != 20 {
		t.Errorf("offset = %v, want 20 (pageno 3)", got)
	}
	if got := body["safesearch"]; got != 1 {
		t.Errorf("safesearch = %v, want 1", got)
	}
}

// TestQwantRequestBodyDefaultLocale checks that the locale defaults to the
// Python default "en_US" when no language is supplied (qwant.py
// traits.get_region default).
func TestQwantRequestBodyDefaultLocale(t *testing.T) {
	e := mustQwantEngine(t, &config.EngineConfig{Name: "test", Engine: "qwant", Shortcut: "qw"})
	params := &engine.RequestParams{}
	if err := e.Request("test", params); err != nil {
		t.Fatalf("Request returned error: %v", err)
	}
	body, ok := params.JSON.(map[string]any)
	if !ok {
		t.Fatalf("JSON = %T, want map[string]any", params.JSON)
	}
	if got := body["locale"]; got != "en_US" {
		t.Errorf("locale = %v, want %q", got, "en_US")
	}
	if got := body["offset"]; got != 0 {
		t.Errorf("offset = %v, want 0 (pageno 1)", got)
	}
}

// TestQwantResponseParse checks response parsing: row-type filtering ("ads"
// rows dropped), item-level _type filtering, and skipping of invalid items
// (qwant.py L156-181).
func TestQwantResponseParse(t *testing.T) {
	body := `{
  "status": "success",
  "data": {
    "result": {
      "items": {
        "mainline": [
          {
            "type": "web",
            "items": [
              {"_type": "web", "url": "https://example.org/a", "title": "Title A", "desc": "Desc A"}
            ]
          },
          {
            "type": "ads",
            "items": [
              {"_type": "web", "url": "https://example.org/ad", "title": "Ad", "desc": "ad"}
            ]
          },
          {
            "type": "web",
            "items": [
              {"_type": "image", "url": "https://example.org/img", "title": "Image", "desc": "img"},
              {"_type": "web", "url": "https://example.org/b", "title": "Title B", "desc": "Desc B"},
              {"_type": "web", "url": "", "title": "No URL", "desc": "skipped"}
            ]
          }
        ]
      }
    }
  }
}`
	e := mustQwantEngine(t, &config.EngineConfig{Name: "test", Engine: "qwant", Shortcut: "qw"})
	got, err := e.Response(respBody(body))
	if err != nil {
		t.Fatalf("Response returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
	if got[0].Kind != result.KindMain {
		t.Fatalf("result 0 Kind = %v, want KindMain", got[0].Kind)
	}
	if got[0].Main.URL != "https://example.org/a" {
		t.Errorf("result 0 URL = %q", got[0].Main.URL)
	}
	if got[0].Main.Title != "Title A" {
		t.Errorf("result 0 Title = %q", got[0].Main.Title)
	}
	if got[0].Main.Content != "Desc A" {
		t.Errorf("result 0 Content = %q, want %q", got[0].Main.Content, "Desc A")
	}
	if got[1].Main.URL != "https://example.org/b" {
		t.Errorf("result 1 URL = %q", got[1].Main.URL)
	}
	if got[1].Main.Title != "Title B" {
		t.Errorf("result 1 Title = %q", got[1].Main.Title)
	}
	if got[1].Main.Content != "Desc B" {
		t.Errorf("result 1 Content = %q, want %q", got[1].Main.Content, "Desc B")
	}
}

// TestQwantResponseNonSuccess checks that a non-"success" status yields an
// empty result set (the .py raises typed exceptions; here documented as a
// defensive empty result, EC-011).
func TestQwantResponseNonSuccess(t *testing.T) {
	body := `{"status": "fail", "data": {"result": {"items": {"mainline": []}}}}`
	e := mustQwantEngine(t, &config.EngineConfig{Name: "test", Engine: "qwant", Shortcut: "qw"})
	got, err := e.Response(respBody(body))
	if err != nil {
		t.Fatalf("Response returned error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d results, want 0", len(got))
	}
}

// TestQwantResponseMissingMainline checks that a response without
// data.result.items.mainline yields an empty result set.
func TestQwantResponseMissingMainline(t *testing.T) {
	body := `{"status": "success", "data": {}}`
	e := mustQwantEngine(t, &config.EngineConfig{Name: "test", Engine: "qwant", Shortcut: "qw"})
	got, err := e.Response(respBody(body))
	if err != nil {
		t.Fatalf("Response returned error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d results, want 0", len(got))
	}
}

// TestNewQwantEngineNilConfig checks the constructor's nil-config guard.
func TestNewQwantEngineNilConfig(t *testing.T) {
	if _, err := NewQwantEngine(nil); !errors.Is(err, engine.ErrInvalidConfig) {
		t.Errorf("NewQwantEngine(nil) error = %v, want ErrInvalidConfig", err)
	}
}
