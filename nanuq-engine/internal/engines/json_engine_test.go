package engines

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
	"nanuq-engine/internal/result"
)

// respBody wraps an inline JSON fixture as an *http.Response body.
func respBody(s string) *http.Response {
	return &http.Response{Body: io.NopCloser(strings.NewReader(s))}
}

func mustEngine(t *testing.T, overrides map[string]any) engine.Engine {
	t.Helper()
	cfg := &config.EngineConfig{Name: "test", Engine: "json_engine", Overrides: overrides}
	eng, err := NewJSONEngine(cfg)
	if err != nil {
		t.Fatalf("NewJSONEngine: %v", err)
	}
	return eng
}

func TestMissingRequiredAttr(t *testing.T) {
	cases := []struct {
		name      string
		overrides map[string]any
	}{
		{"both missing", nil},
		{"search_url only", map[string]any{"search_url": "https://x/?q={query}"}},
		{"results_query only", map[string]any{"results_query": "results"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &config.EngineConfig{Name: "test", Engine: "json_engine", Overrides: c.overrides}
			_, err := NewJSONEngine(cfg)
			if err == nil {
				t.Fatal("expected error for missing required attribute")
			}
			if !errors.Is(err, engine.ErrInvalidConfig) {
				t.Fatalf("error %v does not wrap engine.ErrInvalidConfig", err)
			}
		})
	}
}

func TestRegister(t *testing.T) {
	reg := engine.New()
	if err := Register(reg); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !reg.Has("json_engine") {
		t.Fatal("json_engine not registered")
	}
	// Duplicate registration must be rejected.
	if err := Register(reg); err == nil {
		t.Fatal("expected error on duplicate registration")
	}
	// Instantiate through the registry with a valid config.
	cfg := config.EngineConfig{
		Name: "mdn", Engine: "json_engine",
		Overrides: map[string]any{
			"search_url":    "https://x/?q={query}",
			"results_query": "documents",
			"url_query":     "mdn_url",
			"title_query":   "title",
		},
	}
	eng, err := reg.Instantiate(cfg)
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	if eng.Name() != "mdn" || eng.Shortcut() != "" {
		t.Fatalf("Name()=%q Shortcut()=%q, want mdn/", eng.Name(), eng.Shortcut())
	}
}

func TestRequestURLFormat(t *testing.T) {
	base := map[string]any{
		"search_url":    "https://example.org/search?q={query}&page={pageno}&lang={lang}",
		"results_query": "documents",
		"url_query":     "mdn_url",
		"title_query":   "title",
	}

	t.Run("first page lang all", func(t *testing.T) {
		eng := mustEngine(t, base)
		params := &engine.RequestParams{Pageno: 1, Language: "all"}
		if err := eng.Request("hello world", params); err != nil {
			t.Fatalf("Request: %v", err)
		}
		want := "https://example.org/search?q=hello+world&page=1&lang=en"
		if params.URL != want {
			t.Fatalf("URL = %q, want %q", params.URL, want)
		}
		if params.Method != "GET" {
			t.Fatalf("Method = %q, want GET", params.Method)
		}
	})

	t.Run("paged language", func(t *testing.T) {
		overrides := map[string]any{}
		for k, v := range base {
			overrides[k] = v
		}
		overrides["page_size"] = 20
		overrides["first_page_num"] = 0
		eng := mustEngine(t, overrides)
		params := &engine.RequestParams{Pageno: 3, Language: "es-ES"}
		if err := eng.Request("hola", params); err != nil {
			t.Fatalf("Request: %v", err)
		}
		// pageno = (3-1)*20+0 = 40, lang = es.
		want := "https://example.org/search?q=hola&page=40&lang=es"
		if params.URL != want {
			t.Fatalf("URL = %q, want %q", params.URL, want)
		}
	})

	t.Run("no page num on first page", func(t *testing.T) {
		overrides := map[string]any{}
		for k, v := range base {
			overrides[k] = v
		}
		overrides["send_page_num_on_first_page"] = false
		eng := mustEngine(t, overrides)
		params := &engine.RequestParams{Pageno: 1, Language: "all"}
		if err := eng.Request("q", params); err != nil {
			t.Fatalf("Request: %v", err)
		}
		want := "https://example.org/search?q=q&page=&lang=en"
		if params.URL != want {
			t.Fatalf("URL = %q, want %q", params.URL, want)
		}
	})

	t.Run("method headers cookies", func(t *testing.T) {
		overrides := map[string]any{
			"search_url":    "https://x/?q={query}",
			"results_query": "results",
			"url_query":     "url",
			"title_query":   "title",
			"method":        "POST",
			"headers":       map[string]any{"X-Engine": "json", "Accept": "application/json"},
			"cookies":       map[string]any{"pref": "on"},
		}
		eng := mustEngine(t, overrides)
		params := &engine.RequestParams{Pageno: 1, Language: "all"}
		if err := eng.Request("q", params); err != nil {
			t.Fatalf("Request: %v", err)
		}
		if params.Method != "POST" {
			t.Fatalf("Method = %q, want POST", params.Method)
		}
		if params.Headers == nil || params.Headers.Get("X-Engine") != "json" || params.Headers.Get("Accept") != "application/json" {
			t.Fatalf("Headers = %v, want X-Engine: json + Accept: application/json", params.Headers)
		}
		if len(params.Cookies) != 1 || params.Cookies[0].Name != "pref" || params.Cookies[0].Value != "on" {
			t.Fatalf("Cookies = %v, want pref=on", params.Cookies)
		}
	})
}

func TestResponseParse(t *testing.T) {
	eng := mustEngine(t, map[string]any{
		"search_url":       "https://x/?q={query}",
		"results_query":    "documents",
		"url_query":        "mdn_url",
		"url_prefix":       "https://developer.mozilla.org",
		"title_query":      "title",
		"content_query":    "summary",
		"thumbnail_query":  "thumb",
		"thumbnail_prefix": "https://cdn/",
	})

	fixture := `{"documents": [
		{"mdn_url": "/docs/a", "title": "A", "summary": "About A", "thumb": "img/a.png"},
		{"mdn_url": "/docs/b", "title": "B", "summary": "About B", "thumb": "img/b.png"}
	]}`
	got, err := eng.Response(respBody(fixture))
	if err != nil {
		t.Fatalf("Response: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}

	want := []struct{ url, title, content, thumb string }{
		{"https://developer.mozilla.org/docs/a", "A", "About A", "https://cdn/img/a.png"},
		{"https://developer.mozilla.org/docs/b", "B", "About B", "https://cdn/img/b.png"},
	}
	for i, w := range want {
		r := got[i]
		if r.Kind != result.KindMain || r.Main == nil {
			t.Fatalf("result[%d]: kind=%v, want KindMain with Main", i, r.Kind)
		}
		if r.Main.URL != w.url || r.Main.Title != w.title || r.Main.Content != w.content || r.Main.Thumbnail != w.thumb {
			t.Fatalf("result[%d] = %+v, want url=%q title=%q content=%q thumb=%q",
				i, r.Main, w.url, w.title, w.content, w.thumb)
		}
	}
}

func TestResponseTitleHTMLToText(t *testing.T) {
	eng := mustEngine(t, map[string]any{
		"search_url":           "https://x/?q={query}",
		"results_query":        "results",
		"url_query":            "url",
		"title_query":          "title",
		"title_html_to_text":   true,
		"content_query":        "content",
		"content_html_to_text": true,
	})

	fixture := `{"results": [{
		"url": "https://a",
		"title": "<style>.x{}</style><span>Hello &amp; World</span>",
		"content": "<p>Line one<br>Line two</p>"
	}]}`
	got, err := eng.Response(respBody(fixture))
	if err != nil {
		t.Fatalf("Response: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d results, want 1", len(got))
	}
	if got[0].Main.Title != "Hello & World" {
		t.Fatalf("Title = %q, want %q", got[0].Main.Title, "Hello & World")
	}
	if got[0].Main.Content != "Line one Line two" {
		t.Fatalf("Content = %q, want %q", got[0].Main.Content, "Line one Line two")
	}
}

func TestResponseNoHTMLToText(t *testing.T) {
	// Without title_html_to_text the raw title must be kept untouched.
	eng := mustEngine(t, map[string]any{
		"search_url":    "https://x/?q={query}",
		"results_query": "results",
		"url_query":     "url",
		"title_query":   "title",
	})
	got, err := eng.Response(respBody(`{"results": [{"url": "https://a", "title": "<b>raw</b>"}]}`))
	if err != nil {
		t.Fatalf("Response: %v", err)
	}
	if len(got) != 1 || got[0].Main.Title != "<b>raw</b>" {
		t.Fatalf("Title = %q, want %q", got[0].Main.Title, "<b>raw</b>")
	}
}

func TestResponseMissingURLSkip(t *testing.T) {
	eng := mustEngine(t, map[string]any{
		"search_url":    "https://x/?q={query}",
		"results_query": "results",
		"url_query":     "url",
		"title_query":   "title",
	})
	fixture := `{"results": [
		{"title": "no url"},
		{"url": "https://x.com", "title": "ok"}
	]}`
	got, err := eng.Response(respBody(fixture))
	if err != nil {
		t.Fatalf("Response: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d results, want 1 (item without url must be skipped)", len(got))
	}
	if got[0].Main.URL != "https://x.com" || got[0].Main.Title != "ok" {
		t.Fatalf("result = %+v, want url=https://x.com title=ok", got[0].Main)
	}
}

func TestResponseSuggestion(t *testing.T) {
	eng := mustEngine(t, map[string]any{
		"search_url":       "https://x/?q={query}",
		"results_query":    "documents",
		"url_query":        "url",
		"title_query":      "title",
		"suggestion_query": "suggestions",
	})
	fixture := `{"documents": [{"url": "https://a", "title": "A"}], "suggestions": ["one", "two"]}`
	got, err := eng.Response(respBody(fixture))
	if err != nil {
		t.Fatalf("Response: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d results, want 3 (1 main + 2 suggestions)", len(got))
	}
	if got[0].Kind != result.KindMain || got[0].Main.Title != "A" {
		t.Fatalf("result[0] = kind %v, want KindMain title A", got[0].Kind)
	}
	wantSug := []string{"one", "two"}
	for i, w := range wantSug {
		r := got[1+i]
		if r.Kind != result.KindSuggestion || r.Str == nil || *r.Str != w {
			t.Fatalf("result[%d] = kind %v str %v, want KindSuggestion %q", 1+i, r.Kind, r.Str, w)
		}
	}
}

func TestResponseNoResults(t *testing.T) {
	eng := mustEngine(t, map[string]any{
		"search_url":    "https://x/?q={query}",
		"results_query": "results",
		"url_query":     "url",
		"title_query":   "title",
	})
	// Missing results path -> no results, no error.
	got, err := eng.Response(respBody(`{"other": [1, 2]}`))
	if err != nil {
		t.Fatalf("Response: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d results, want 0", len(got))
	}
	// Empty body -> no results, no error (json_engine.py `if not resp.text`).
	got, err = eng.Response(respBody(""))
	if err != nil {
		t.Fatalf("Response (empty body): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d results for empty body, want 0", len(got))
	}
}

func TestInvalidJSON(t *testing.T) {
	eng := mustEngine(t, map[string]any{
		"search_url":    "https://x/?q={query}",
		"results_query": "results",
		"url_query":     "url",
		"title_query":   "title",
	})
	for name, body := range map[string]string{
		"malformed":        `{invalid`,
		"trailing tokens":  `1 2`,
		"trailing garbage": `{"results": []} garbage`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := eng.Response(respBody(body)); err == nil {
				t.Fatalf("expected error for body %q", body)
			}
		})
	}
}
