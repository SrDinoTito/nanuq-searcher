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

// xpathRespBody wraps an inline HTML fixture as an *http.Response body.
//
// NOTE (deviation): the TASK-009 spec said "http.Response con body
// html.NewReader", but golang.org/x/net/html exposes no html.NewReader (only
// NewTokenizer/NewTokenizerFragment; verified against net@v0.53.0). The
// engine consumes the body via htmlquery.Parse (which internally runs
// html.Parse), so a plain reader is sufficient — mirroring json_engine_test.go's
// respBody helper.
func xpathRespBody(htmlBody string) *http.Response {
	return &http.Response{Body: io.NopCloser(strings.NewReader(htmlBody))}
}

func mustXPathEngine(t *testing.T, overrides map[string]any) engine.Engine {
	t.Helper()
	cfg := &config.EngineConfig{Name: "test", Engine: "xpath", Overrides: overrides}
	eng, err := NewXPathEngine(cfg)
	if err != nil {
		t.Fatalf("NewXPathEngine: %v", err)
	}
	return eng
}

func TestRegisterXPath(t *testing.T) {
	reg := engine.New()
	if err := RegisterXPath(reg); err != nil {
		t.Fatalf("RegisterXPath: %v", err)
	}
	if !reg.Has("xpath") {
		t.Fatal("xpath not registered")
	}
	// RegisterXPath owns ONLY the "xpath" module; "json_engine" is
	// TASK-010's Register (json_engine.go). Collision resolved (Opción B).
	if reg.Has("json_engine") {
		t.Fatal("RegisterXPath must not register json_engine")
	}
	// Duplicate registration must be rejected.
	if err := RegisterXPath(reg); err == nil {
		t.Fatal("expected error on duplicate registration")
	}
	// Instantiate through the registry with a valid config.
	cfg := config.EngineConfig{
		Name: "ddg", Engine: "xpath",
		Overrides: map[string]any{
			"search_url":    "https://x/?q={query}",
			"results_xpath": "//div[@class='result']",
			"url_xpath":     ".//a/@href",
			"title_xpath":   ".//h3",
		},
	}
	eng, err := reg.Instantiate(cfg)
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	if eng.Name() != "ddg" || eng.Shortcut() != "" {
		t.Fatalf("Name()=%q Shortcut()=%q, want ddg/", eng.Name(), eng.Shortcut())
	}
}

// TestXPathRequestURLFormat is named with the XPath prefix because the
// plain name would collide with json_engine_test.go's TestRequestURLFormat
// (both live in package engines).
func TestXPathRequestURLFormat(t *testing.T) {
	base := map[string]any{
		"search_url":    "https://example.org/search?q={query}&page={pageno}&lang={lang}",
		"results_xpath": "//div[@class='result']",
		// paging must be enabled for {pageno} to be filled (xpath.py:
		// paging is an opt-in engine option, default off).
		"paging": true,
	}

	t.Run("first page lang all", func(t *testing.T) {
		eng := mustXPathEngine(t, base)
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

	t.Run("query url encoding", func(t *testing.T) {
		eng := mustXPathEngine(t, base)
		params := &engine.RequestParams{Pageno: 1, Language: "all"}
		if err := eng.Request("a&b=c", params); err != nil {
			t.Fatalf("Request: %v", err)
		}
		want := "https://example.org/search?q=a%26b%3Dc&page=1&lang=en"
		if params.URL != want {
			t.Fatalf("URL = %q, want %q", params.URL, want)
		}
	})

	t.Run("paged language", func(t *testing.T) {
		overrides := map[string]any{}
		for k, v := range base {
			overrides[k] = v
		}
		overrides["page_size"] = 10
		overrides["first_page_num"] = 0
		eng := mustXPathEngine(t, overrides)
		params := &engine.RequestParams{Pageno: 3, Language: "es-ES"}
		if err := eng.Request("hola", params); err != nil {
			t.Fatalf("Request: %v", err)
		}
		// pageno = (3-1)*10+0 = 20, lang = es.
		want := "https://example.org/search?q=hola&page=20&lang=es"
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
		eng := mustXPathEngine(t, overrides)
		params := &engine.RequestParams{Pageno: 1, Language: "all"}
		if err := eng.Request("q", params); err != nil {
			t.Fatalf("Request: %v", err)
		}
		want := "https://example.org/search?q=q&page=&lang=en"
		if params.URL != want {
			t.Fatalf("URL = %q, want %q", params.URL, want)
		}
	})

	t.Run("time range", func(t *testing.T) {
		eng := mustXPathEngine(t, map[string]any{
			"search_url":     "https://example.org/search?q={query}&time={time_range}",
			"results_xpath":  "//div[@class='result']",
			"time_range_url": "hours={time_range_val}",
			"time_range_map": map[string]any{
				"day":  "24",
				"week": "168",
			},
		})
		params := &engine.RequestParams{Pageno: 1, Language: "all", TimeRange: "day"}
		if err := eng.Request("q", params); err != nil {
			t.Fatalf("Request: %v", err)
		}
		want := "https://example.org/search?q=q&time=hours=24"
		if params.URL != want {
			t.Fatalf("URL = %q, want %q", params.URL, want)
		}
		// Unmapped time range -> placeholder blanked.
		params2 := &engine.RequestParams{Pageno: 1, Language: "all", TimeRange: "month"}
		if err := eng.Request("q", params2); err != nil {
			t.Fatalf("Request: %v", err)
		}
		want2 := "https://example.org/search?q=q&time="
		if params2.URL != want2 {
			t.Fatalf("URL = %q, want %q", params2.URL, want2)
		}
	})

	t.Run("safe search", func(t *testing.T) {
		eng := mustXPathEngine(t, map[string]any{
			"search_url":      "https://example.org/search?q={query}&safe={safe_search}",
			"results_xpath":   "//div[@class='result']",
			"safe_search_map": map[string]any{"0": "off", "1": "moderate", "2": "strict"},
		})
		params := &engine.RequestParams{Pageno: 1, Language: "all", SafeSearch: 1}
		if err := eng.Request("q", params); err != nil {
			t.Fatalf("Request: %v", err)
		}
		want := "https://example.org/search?q=q&safe=moderate"
		if params.URL != want {
			t.Fatalf("URL = %q, want %q", params.URL, want)
		}
		// Unmapped safe search -> placeholder blanked.
		params2 := &engine.RequestParams{Pageno: 1, Language: "all", SafeSearch: 5}
		if err := eng.Request("q", params2); err != nil {
			t.Fatalf("Request: %v", err)
		}
		want2 := "https://example.org/search?q=q&safe="
		if params2.URL != want2 {
			t.Fatalf("URL = %q, want %q", params2.URL, want2)
		}
	})

	t.Run("no language support", func(t *testing.T) {
		overrides := map[string]any{}
		for k, v := range base {
			overrides[k] = v
		}
		overrides["language_support"] = false
		eng := mustXPathEngine(t, overrides)
		params := &engine.RequestParams{Pageno: 1, Language: "de"}
		if err := eng.Request("q", params); err != nil {
			t.Fatalf("Request: %v", err)
		}
		want := "https://example.org/search?q=q&page=1&lang="
		if params.URL != want {
			t.Fatalf("URL = %q, want %q", params.URL, want)
		}
	})

	t.Run("method headers cookies", func(t *testing.T) {
		eng := mustXPathEngine(t, map[string]any{
			"search_url":    "https://x/?q={query}",
			"results_xpath": "//div[@class='result']",
			"method":        "POST",
			"headers":       map[string]any{"X-Engine": "xpath", "Accept": "text/html"},
			"cookies":       map[string]any{"pref": "on"},
		})
		params := &engine.RequestParams{Pageno: 1, Language: "all"}
		if err := eng.Request("q", params); err != nil {
			t.Fatalf("Request: %v", err)
		}
		if params.Method != "POST" {
			t.Fatalf("Method = %q, want POST", params.Method)
		}
		if params.Headers == nil || params.Headers.Get("X-Engine") != "xpath" || params.Headers.Get("Accept") != "text/html" {
			t.Fatalf("Headers = %v, want X-Engine: xpath + Accept: text/html", params.Headers)
		}
		if len(params.Cookies) != 1 || params.Cookies[0].Name != "pref" || params.Cookies[0].Value != "on" {
			t.Fatalf("Cookies = %v, want pref=on", params.Cookies)
		}
	})
}

// resultsFixture mirrors the HTML shape of a searxng xpath engine result
// page: per-result containers with relative and absolute URLs, titles,
// snippets and thumbnails. The third container lacks an anchor, so it must
// be skipped (URL not found).
const resultsFixture = `<html><body>
<div class="result">
  <a href="/docs/a">Result A</a>
  <h3>Result A</h3>
  <p>Content of result A</p>
  <img src="/img/a.png"/>
</div>
<div class="result">
  <a href="https://other.example.org/b">Result B</a>
  <h3>Result B</h3>
  <p>Content of result B</p>
  <img src="https://cdn.example.org/b.png"/>
</div>
<div class="result">
  <h3>No URL</h3>
  <p>This result has no link and must be skipped.</p>
</div>
</body></html>`

func TestXPathResponseParse(t *testing.T) {
	eng := mustXPathEngine(t, map[string]any{
		"search_url":      "https://example.org/search?q={query}",
		"results_xpath":   "//div[@class='result']",
		"url_xpath":       ".//a/@href",
		"title_xpath":     ".//h3",
		"content_xpath":   ".//p",
		"thumbnail_xpath": ".//img/@src",
	})

	got, err := eng.Response(xpathRespBody(resultsFixture))
	if err != nil {
		t.Fatalf("Response: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2 (URL-less container must be skipped)", len(got))
	}

	want := []struct{ url, title, content, thumb string }{
		{"https://example.org/docs/a", "Result A", "Content of result A", "https://example.org/img/a.png"},
		{"https://other.example.org/b", "Result B", "Content of result B", "https://cdn.example.org/b.png"},
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

func TestXPathResponseSuggestion(t *testing.T) {
	eng := mustXPathEngine(t, map[string]any{
		"search_url":       "https://x/?q={query}",
		"results_xpath":    "//div[@class='result']",
		"url_xpath":        ".//a/@href",
		"suggestion_xpath": "//p[@class='suggestion']",
	})

	fixture := `<html><body>
<p class="suggestion">suggestion one</p>
<p class="suggestion">suggestion two</p>
<p class="suggestion"></p>
</body></html>`
	got, err := eng.Response(xpathRespBody(fixture))
	if err != nil {
		t.Fatalf("Response: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2 suggestions (empty one skipped)", len(got))
	}
	wantSug := []string{"suggestion one", "suggestion two"}
	for i, w := range wantSug {
		r := got[i]
		if r.Kind != result.KindSuggestion || r.Str == nil || *r.Str != w {
			t.Fatalf("result[%d] = kind %v str %v, want KindSuggestion %q", i, r.Kind, r.Str, w)
		}
	}
}

func TestNoResultForHTTPStatus(t *testing.T) {
	eng := mustXPathEngine(t, map[string]any{
		"search_url":                "https://example.org/search?q={query}",
		"results_xpath":             "//div[@class='result']",
		"url_xpath":                 ".//a/@href",
		"no_result_for_http_status": []any{429, 503},
	})

	// Status in the list -> no results, no error (xpath.py early return).
	resp := &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader(resultsFixture))}
	got, err := eng.Response(resp)
	if err != nil {
		t.Fatalf("Response (503): %v", err)
	}
	if got != nil {
		t.Fatalf("got %d results for status 503, want nil", len(got))
	}

	// Status outside the list -> parsed normally.
	for _, status := range []int{200, 404} {
		resp := &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(resultsFixture))}
		got, err := eng.Response(resp)
		if err != nil {
			t.Fatalf("Response (%d): %v", status, err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d results for status %d, want 2", len(got), status)
		}
	}
}

func TestXPathMissingRequiredAttr(t *testing.T) {
	cases := []struct {
		name      string
		overrides map[string]any
	}{
		{"both missing", nil},
		{"search_url only", map[string]any{"results_xpath": "//div[@class='result']"}},
		{"results_xpath only", map[string]any{"search_url": "https://x/?q={query}"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &config.EngineConfig{Name: "test", Engine: "xpath", Overrides: c.overrides}
			_, err := NewXPathEngine(cfg)
			if err == nil {
				t.Fatal("expected error for missing required attribute")
			}
			if !errors.Is(err, engine.ErrInvalidConfig) {
				t.Fatalf("error %v does not wrap engine.ErrInvalidConfig", err)
			}
		})
	}
}

func TestXPathInvalid(t *testing.T) {
	cases := []struct {
		name, key string
		overrides map[string]any
	}{
		{
			"results_xpath", "results_xpath",
			map[string]any{
				"search_url":    "https://x/?q={query}",
				"results_xpath": "//div[@class='",
			},
		},
		{
			"title_xpath", "title_xpath",
			map[string]any{
				"search_url":    "https://x/?q={query}",
				"results_xpath": "//div[@class='result']",
				"title_xpath":   "//a[",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &config.EngineConfig{Name: "test", Engine: "xpath", Overrides: c.overrides}
			_, err := NewXPathEngine(cfg)
			if err == nil {
				t.Fatalf("expected error for invalid XPath override %q", c.key)
			}
			if !errors.Is(err, engine.ErrInvalidConfig) {
				t.Fatalf("error %v does not wrap engine.ErrInvalidConfig", err)
			}
			if !strings.Contains(err.Error(), c.key) {
				t.Fatalf("error %v does not mention override %q", err, c.key)
			}
		})
	}
}
