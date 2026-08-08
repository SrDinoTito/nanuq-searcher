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

// baiduRespBody wraps an inline fixture as an *http.Response body with the
// given status code and headers (the captcha check reads the Location
// header, baidu.py L130).
func baiduRespBody(body string, status int, hdr http.Header) *http.Response {
	return &http.Response{StatusCode: status, Header: hdr, Body: io.NopCloser(strings.NewReader(body))}
}

func mustBaiduEngine(t *testing.T, overrides map[string]any) engine.Engine {
	t.Helper()
	cfg := &config.EngineConfig{Name: "baidu_test", Engine: "baidu", Overrides: overrides}
	eng, err := NewBaiduEngine(cfg)
	if err != nil {
		t.Fatalf("NewBaiduEngine: %v", err)
	}
	return eng
}

// TestBaiduNilConfig verifies the construction guards (EC-011) wrapping
// engine.ErrInvalidConfig: nil config and unsupported category overrides.
func TestBaiduNilConfig(t *testing.T) {
	if _, err := NewBaiduEngine(nil); !errors.Is(err, engine.ErrInvalidConfig) {
		t.Fatalf("NewBaiduEngine(nil) error = %v, want wrap of engine.ErrInvalidConfig", err)
	}
	for _, cat := range []string{"images", "it", "news"} {
		if _, err := NewBaiduEngine(&config.EngineConfig{Name: "bd", Overrides: map[string]any{
			"category": cat,
		}}); !errors.Is(err, engine.ErrInvalidConfig) {
			t.Fatalf("category %q error = %v, want wrap of engine.ErrInvalidConfig", cat, err)
		}
	}
}

// TestBaiduRequestURLFormat ports the url assertions of request() for the
// general category (baidu.py L73-125): wd/rn/pn/tn params in Python dict
// order, paging offset, time_range gpc and the results_per_page override.
func TestBaiduRequestURLFormat(t *testing.T) {
	cases := []struct {
		name       string
		query      string
		pageno     int
		timeRange  string
		override   map[string]any
		wantPrefix string
		wantSuffix string
	}{
		{
			name:       "first page",
			query:      "hello world",
			pageno:     1,
			wantPrefix: "https://www.baidu.com/s?wd=hello+world&rn=10&pn=0&tn=json",
		},
		{
			name:       "second page offset",
			query:      "go",
			pageno:     2,
			wantPrefix: "https://www.baidu.com/s?wd=go&rn=10&pn=10&tn=json",
		},
		{
			name:       "pageno zero clamped",
			query:      "go",
			pageno:     0,
			wantPrefix: "https://www.baidu.com/s?wd=go&rn=10&pn=0&tn=json",
		},
		{
			name:       "time range appends gpc",
			query:      "go",
			pageno:     1,
			timeRange:  "day",
			wantPrefix: "https://www.baidu.com/s?wd=go&rn=10&pn=0&tn=json&gpc=stf=",
			wantSuffix: "|stftype=1",
		},
		{
			name:       "unknown time range ignored",
			query:      "go",
			pageno:     1,
			timeRange:  "hour",
			wantPrefix: "https://www.baidu.com/s?wd=go&rn=10&pn=0&tn=json",
		},
		{
			name:       "results_per_page override",
			query:      "go",
			pageno:     2,
			override:   map[string]any{"results_per_page": 20},
			wantPrefix: "https://www.baidu.com/s?wd=go&rn=20&pn=20&tn=json",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			eng := mustBaiduEngine(t, c.override)
			params := &engine.RequestParams{Pageno: c.pageno, TimeRange: c.timeRange}
			if err := eng.Request(c.query, params); err != nil {
				t.Fatalf("Request: %v", err)
			}
			if !strings.HasPrefix(params.URL, c.wantPrefix) {
				t.Fatalf("URL = %q, want prefix %q", params.URL, c.wantPrefix)
			}
			if c.wantSuffix != "" && !strings.HasSuffix(params.URL, c.wantSuffix) {
				t.Fatalf("URL = %q, want suffix %q", params.URL, c.wantSuffix)
			}
			if c.wantSuffix == "" && params.URL != c.wantPrefix {
				t.Fatalf("URL = %q, want %q", params.URL, c.wantPrefix)
			}
			if params.Method != "GET" {
				t.Fatalf("Method = %q, want GET", params.Method)
			}
		})
	}
}

// TestBaiduResponseParse ports parse_general() (baidu.py L145-173): entries
// without title or url are skipped, HTML entities are unescaped.
func TestBaiduResponseParse(t *testing.T) {
	body := `{
		"feed": {"entry": [
			{"title": "Baidu &amp; 百科", "url": "https://baike.baidu.com/item/x",
			 "abs": "Desc &quot;quoted&quot;", "time": 1700000000},
			{"title": "no url"},
			{"url": "https://example.org/no-title"}
		]}
	}`
	eng := mustBaiduEngine(t, nil)
	results, err := eng.Response(baiduRespBody(body, http.StatusOK, nil))
	if err != nil {
		t.Fatalf("Response: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	main := results[0].Main
	if results[0].Kind != result.KindMain || main == nil {
		t.Fatalf("results[0] = %+v, want KindMain", results[0])
	}
	if main.Title != "Baidu & 百科" || main.URL != "https://baike.baidu.com/item/x" ||
		main.Content != `Desc "quoted"` {
		t.Fatalf("main = %+v", main)
	}
}

// TestBaiduLenientJSON ports json.loads(strict=False) (baidu.py L137): raw
// control characters inside string literals are legal in Python's lenient
// mode and are escaped by escapeControlChars before decoding.
func TestBaiduLenientJSON(t *testing.T) {
	// \x01 is a literal raw control byte inside the JSON string (Go string
	// escape \x01 produces it directly).
	body := "{\"feed\": {\"entry\": [{\"title\": \"a\x01b\", \"url\": \"u\"}]}}"
	eng := mustBaiduEngine(t, nil)
	results, err := eng.Response(baiduRespBody(body, http.StatusOK, nil))
	if err != nil {
		t.Fatalf("Response: %v", err)
	}
	if len(results) != 1 || results[0].Main.Title != "a\x01b" {
		t.Fatalf("results = %+v, want title with preserved control byte", results)
	}
}

// TestBaiduCaptcha ports the wappass redirect detection (baidu.py L129-131).
func TestBaiduCaptcha(t *testing.T) {
	eng := mustBaiduEngine(t, nil)
	hdr := http.Header{"Location": []string{"https://wappass.baidu.com/static/captcha?rid=123"}}
	_, err := eng.Response(baiduRespBody("{}", http.StatusFound, hdr))
	if !errors.Is(err, errBaiduCaptcha) {
		t.Fatalf("Response(captcha) error = %v, want wrap of errBaiduCaptcha", err)
	}
}

// TestBaiduAntiFlag ports the access-denial detection (baidu.py L138-139).
func TestBaiduAntiFlag(t *testing.T) {
	eng := mustBaiduEngine(t, nil)

	t.Run("message from server", func(t *testing.T) {
		_, err := eng.Response(baiduRespBody(`{"antiFlag": 1, "message": "blocked"}`, http.StatusOK, nil))
		if !errors.Is(err, errBaiduAccessDenied) || !strings.Contains(err.Error(), "blocked") {
			t.Fatalf("Response(antiFlag) error = %v, want wrap of errBaiduAccessDenied with server message", err)
		}
	})
	t.Run("default message", func(t *testing.T) {
		_, err := eng.Response(baiduRespBody(`{"antiFlag": 1}`, http.StatusOK, nil))
		if !errors.Is(err, errBaiduAccessDenied) || !strings.Contains(err.Error(), "Forbid spider access") {
			t.Fatalf("Response(antiFlag) error = %v, want wrap of errBaiduAccessDenied with default message", err)
		}
	})
	t.Run("antiFlag zero passes through", func(t *testing.T) {
		results, err := eng.Response(baiduRespBody(`{"antiFlag": 0, "feed": {"entry": []}}`, http.StatusOK, nil))
		if err != nil || len(results) != 0 {
			t.Fatalf("Response(antiFlag 0) = %v, %v; want empty, nil", results, err)
		}
	})
}

// TestBaiduInvalidResponse ports the SearxEngineAPIException paths
// (baidu.py L147-148) and the invalid-JSON error.
func TestBaiduInvalidResponse(t *testing.T) {
	eng := mustBaiduEngine(t, nil)

	t.Run("missing feed", func(t *testing.T) {
		if _, err := eng.Response(baiduRespBody(`{"foo": 1}`, http.StatusOK, nil)); err == nil {
			t.Fatal("Response(missing feed) error = nil, want error")
		}
	})
	t.Run("missing entry", func(t *testing.T) {
		if _, err := eng.Response(baiduRespBody(`{"feed": {}}`, http.StatusOK, nil)); err == nil {
			t.Fatal("Response(missing entry) error = nil, want error")
		}
	})
	t.Run("invalid json", func(t *testing.T) {
		if _, err := eng.Response(baiduRespBody("not json", http.StatusOK, nil)); err == nil {
			t.Fatal("Response(garbage) error = nil, want error")
		}
	})
}
