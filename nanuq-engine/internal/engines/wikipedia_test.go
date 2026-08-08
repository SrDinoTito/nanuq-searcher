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

// wikiRespBody wraps an inline fixture as an *http.Response body with the
// given status code (needed by the 404/400 short-circuits of wikipedia.py
// response(), L167-180).
func wikiRespBody(body string, status int) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}
}

func mustWikiEngine(t *testing.T, overrides map[string]any) engine.Engine {
	t.Helper()
	cfg := &config.EngineConfig{Name: "wiki_test", Engine: "wikipedia", Overrides: overrides}
	eng, err := NewWikipediaEngine(cfg)
	if err != nil {
		t.Fatalf("NewWikipediaEngine: %v", err)
	}
	return eng
}

// TestWikiNilConfig verifies the construction guard (EC-011) wrapping
// engine.ErrInvalidConfig.
func TestWikiNilConfig(t *testing.T) {
	if _, err := NewWikipediaEngine(nil); !errors.Is(err, engine.ErrInvalidConfig) {
		t.Fatalf("NewWikipediaEngine(nil) error = %v, want wrap of engine.ErrInvalidConfig", err)
	}
	if _, err := NewWikipediaEngine(&config.EngineConfig{Name: "wp", Overrides: map[string]any{
		"display_type": "bogus",
	}}); !errors.Is(err, engine.ErrInvalidConfig) {
		t.Fatalf("bad display_type error = %v, want wrap of engine.ErrInvalidConfig", err)
	}
}

// TestWikiRequestURLFormat ports the url assertions of the request() step
// (wikipedia.py L148-160): str.title() on lowercase queries, quote() of the
// title, and the netloc resolution.
func TestWikiRequestURLFormat(t *testing.T) {
	cases := []struct {
		name     string
		query    string
		language string
		override map[string]any
		want     string
	}{
		{
			name:  "lowercase title-cased",
			query: "hello world",
			want:  "https://en.wikipedia.org/api/rest_v1/page/summary/Hello%20World",
		},
		{
			name:  "mixed case untouched",
			query: "Hello World",
			want:  "https://en.wikipedia.org/api/rest_v1/page/summary/Hello%20World",
		},
		{
			name:     "spanish language",
			query:    "Barcelona",
			language: "es",
			want:     "https://es.wikipedia.org/api/rest_v1/page/summary/Barcelona",
		},
		{
			name:     "region variant collapses to language",
			query:    "出租車",
			language: "zh-CN",
			want:     "https://zh.wikipedia.org/api/rest_v1/page/summary/%E5%87%BA%E7%A7%9F%E8%BB%8A",
		},
		{
			name:     "language all falls back to default",
			query:    "foo",
			language: "all",
			want:     "https://en.wikipedia.org/api/rest_v1/page/summary/Foo",
		},
		{
			name:     "language override wins when no language requested",
			query:    "foo",
			override: map[string]any{"language": "de"},
			want:     "https://de.wikipedia.org/api/rest_v1/page/summary/Foo",
		},
		{
			name:  "slash escaped by PathEscape",
			query: "a/b",
			want:  "https://en.wikipedia.org/api/rest_v1/page/summary/A%2FB",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			eng := mustWikiEngine(t, c.override)
			params := &engine.RequestParams{Language: c.language}
			if err := eng.Request(c.query, params); err != nil {
				t.Fatalf("Request: %v", err)
			}
			if params.URL != c.want {
				t.Fatalf("URL = %q, want %q", params.URL, c.want)
			}
			if params.Method != "GET" {
				t.Fatalf("Method = %q, want GET", params.Method)
			}
		})
	}
}

// TestWikiResponseParse ports response() (wikipedia.py L164-210) with an
// inline REST v1 summary fixture.
func TestWikiResponseParse(t *testing.T) {
	const standard = `{
		"type": "standard",
		"title": "Monty Python",
		"titles": {"display": "Monty Python"},
		"content_urls": {"desktop": {"page": "https://en.wikipedia.org/wiki/Monty_Python"}},
		"description": "British comedy group",
		"extract": "Monty Python was a British comedy troupe.",
		"thumbnail": {"source": "https://upload.wikimedia.org/thumb.jpg"}
	}`

	t.Run("default infobox display yields infobox only", func(t *testing.T) {
		// wikipedia.py L77: display_type defaults to ["infobox"], so a
		// standard page produces ONLY the infobox (L198-208); the 'list'
		// branch (L187-196) is skipped.
		eng := mustWikiEngine(t, nil)
		results, err := eng.Response(wikiRespBody(standard, http.StatusOK))
		if err != nil {
			t.Fatalf("Response: %v", err)
		}
		if len(results) != 1 || results[0].Kind != result.KindInfobox {
			t.Fatalf("results = %+v, want a single KindInfobox", results)
		}
		ib := results[0].Infobox
		if ib == nil {
			t.Fatal("results[0].Infobox is nil")
		}
		if ib.Title != "Monty Python" || ib.Content != "Monty Python was a British comedy troupe." ||
			ib.ImgSrc != "https://upload.wikimedia.org/thumb.jpg" ||
			len(ib.URLs) != 1 || ib.URLs[0] != "https://en.wikipedia.org/wiki/Monty_Python" {
			t.Fatalf("infobox = %+v", ib)
		}
	})

	t.Run("list and infobox display yields both", func(t *testing.T) {
		eng := mustWikiEngine(t, map[string]any{"display_type": []any{"list", "infobox"}})
		results, err := eng.Response(wikiRespBody(standard, http.StatusOK))
		if err != nil {
			t.Fatalf("Response: %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("len(results) = %d, want 2", len(results))
		}
		main := results[0].Main
		if results[0].Kind != result.KindMain || main == nil {
			t.Fatalf("results[0] = %+v, want KindMain", results[0])
		}
		if main.URL != "https://en.wikipedia.org/wiki/Monty_Python" || main.Title != "Monty Python" ||
			main.Content != "British comedy group" {
			t.Fatalf("main = %+v", main)
		}
		ib := results[1].Infobox
		if results[1].Kind != result.KindInfobox || ib == nil {
			t.Fatalf("results[1] = %+v, want KindInfobox", results[1])
		}
	})

	t.Run("list display yields main only", func(t *testing.T) {
		eng := mustWikiEngine(t, map[string]any{"display_type": []any{"list"}})
		results, err := eng.Response(wikiRespBody(standard, http.StatusOK))
		if err != nil {
			t.Fatalf("Response: %v", err)
		}
		if len(results) != 1 || results[0].Kind != result.KindMain {
			t.Fatalf("results = %+v, want a single KindMain", results)
		}
	})

	t.Run("non-standard page falls back to main", func(t *testing.T) {
		body := strings.Replace(standard, `"type": "standard"`, `"type": "disambiguation"`, 1)
		eng := mustWikiEngine(t, nil) // default display ["infobox"]
		results, err := eng.Response(wikiRespBody(body, http.StatusOK))
		if err != nil {
			t.Fatalf("Response: %v", err)
		}
		if len(results) != 1 || results[0].Kind != result.KindMain {
			t.Fatalf("results = %+v, want a single KindMain", results)
		}
	})

	t.Run("html_to_text on display title", func(t *testing.T) {
		body := strings.Replace(standard, `"display": "Monty Python"`, `"display": "Monty &amp; Python"`, 1)
		eng := mustWikiEngine(t, nil)
		results, err := eng.Response(wikiRespBody(body, http.StatusOK))
		if err != nil {
			t.Fatalf("Response: %v", err)
		}
		if len(results) != 1 || results[0].Kind != result.KindInfobox ||
			results[0].Infobox.Title != "Monty & Python" {
			t.Fatalf("title = %+v, want 'Monty & Python'", results)
		}
	})
}

// TestWikiResponseStatuses ports the status short-circuits of response()
// (wikipedia.py L167-180) and raise_for_httperror (L181).
func TestWikiResponseStatuses(t *testing.T) {
	eng := mustWikiEngine(t, nil)

	t.Run("404 empty", func(t *testing.T) {
		results, err := eng.Response(wikiRespBody("{}", http.StatusNotFound))
		if err != nil || results != nil {
			t.Fatalf("Response(404) = %v, %v; want nil, nil", results, err)
		}
	})

	t.Run("400 title-invalid-characters empty", func(t *testing.T) {
		body := `{
			"type": "https://mediawiki.org/wiki/HyperSwitch/errors/bad_request",
			"detail": "title-invalid-characters"
		}`
		results, err := eng.Response(wikiRespBody(body, http.StatusBadRequest))
		if err != nil || results != nil {
			t.Fatalf("Response(400 bad_request) = %v, %v; want nil, nil", results, err)
		}
	})

	t.Run("400 unrelated body falls through to error", func(t *testing.T) {
		_, err := eng.Response(wikiRespBody("{}", http.StatusBadRequest))
		if err == nil {
			t.Fatal("Response(400 unrelated) error = nil, want error (raise_for_httperror)")
		}
	})

	t.Run("500 raises", func(t *testing.T) {
		_, err := eng.Response(wikiRespBody("{}", http.StatusInternalServerError))
		if err == nil {
			t.Fatal("Response(500) error = nil, want error (raise_for_httperror)")
		}
	})

	t.Run("invalid JSON errors", func(t *testing.T) {
		if _, err := eng.Response(wikiRespBody("not json", http.StatusOK)); err == nil {
			t.Fatal("Response(garbage) error = nil, want error")
		}
	})
}
