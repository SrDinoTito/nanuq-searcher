package engines

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
	"nanuq-engine/internal/result"
)

// newDuckDuckGo builds a DuckDuckGo engine instance from a YAML entry.
func newDuckDuckGo(t *testing.T, cfg *config.EngineConfig) engine.Engine {
	t.Helper()
	eng, err := NewDuckDuckGoEngine(cfg)
	if err != nil {
		t.Fatalf("NewDuckDuckGoEngine: %v", err)
	}
	return eng
}

// TestDuckDuckGoRequestURLFormat verifies the request building of every mode
// (html web, extra images/videos/news, weather) — the 1:N instance matrix.
func TestDuckDuckGoRequestURLFormat(t *testing.T) {
	t.Run("duckduckgo general html mode", func(t *testing.T) {
		eng := newDuckDuckGo(t, &config.EngineConfig{
			Name: "duckduckgo", Engine: "duckduckgo",
			Categories: []string{"general", "web"},
		})
		params := &engine.RequestParams{Headers: http.Header{}, Data: map[string]string{}, Pageno: 1, Language: "en"}
		if err := eng.Request("hello world", params); err != nil {
			t.Fatalf("Request: %v", err)
		}
		// duckduckgo.py L403-404: POST to the DDG-html endpoint.
		if params.Method != http.MethodPost || params.URL != "https://html.duckduckgo.com/html/" {
			t.Fatalf("Method=%q URL=%q, want POST / html.duckduckgo.com/html/", params.Method, params.URL)
		}
		// duckduckgo.py L402 + L406-407: q + first-page b="".
		if params.Data["q"] != "hello world" || params.Data["b"] != "" {
			t.Fatalf("Data[q]=%q Data[b]=%q, want query and empty b", params.Data["q"], params.Data["b"])
		}
		// duckduckgo.py L438-444: kl is always "wt-wt" without traits.
		if params.Data["kl"] != "wt-wt" {
			t.Fatalf("Data[kl]=%q, want wt-wt", params.Data["kl"])
		}
		// duckduckgo.py L384-389: Sec-Fetch + Referer headers.
		if got := params.Headers.Get("Sec-Fetch-Mode"); got != "navigate" {
			t.Fatalf("Sec-Fetch-Mode=%q, want navigate", got)
		}
		if got := params.Headers.Get("Referer"); got != "https://html.duckduckgo.com/html/" {
			t.Fatalf("Referer=%q, want ddg_url", got)
		}
		if got := params.Headers.Get("Accept-Language"); got != "en,en-EN;q=0.7" {
			t.Fatalf("Accept-Language=%q, want en,en-EN;q=0.7", got)
		}
		// duckduckgo.py L346-359: bang tokens are quoted.
		params2 := &engine.RequestParams{Headers: http.Header{}, Data: map[string]string{}, Pageno: 1}
		if err := eng.Request("!g rust", params2); err != nil {
			t.Fatalf("Request(bang): %v", err)
		}
		if params2.Data["q"] != "'!g' rust" {
			t.Fatalf("Data[q]=%q, want quoted bang", params2.Data["q"])
		}
	})

	t.Run("duckduckgo_web routes to html mode", func(t *testing.T) {
		// duckduckgo_web.py needs I/O in request() (deep_preload_link
		// scrape); the Go contract routes general web instances to the
		// shared no-JS HTML flow (file header deviation note).
		eng := newDuckDuckGo(t, &config.EngineConfig{
			Name: "duckduckgo web", Engine: "duckduckgo_web",
			Categories: []string{"general"},
		})
		params := &engine.RequestParams{Headers: http.Header{}, Data: map[string]string{}, Pageno: 1}
		if err := eng.Request("rust", params); err != nil {
			t.Fatalf("Request: %v", err)
		}
		if params.URL != "https://html.duckduckgo.com/html/" || params.Method != http.MethodPost {
			t.Fatalf("URL=%q Method=%q, want html endpoint POST", params.URL, params.Method)
		}
	})

	t.Run("images extra mode", func(t *testing.T) {
		eng := newDuckDuckGo(t, &config.EngineConfig{
			Name: "duckduckgo images", Engine: "duckduckgo_extra",
			Categories: []string{"images"},
			Overrides:  map[string]any{"ddg_category": "images"},
		})
		params := &engine.RequestParams{Headers: http.Header{}, Pageno: 1, SafeSearch: 2}
		if err := eng.Request("hello world", params); err != nil {
			t.Fatalf("Request: %v", err)
		}
		// duckduckgo_extra.py L143: i.js endpoint.
		if !strings.HasPrefix(params.URL, "https://duckduckgo.com/i.js?") {
			t.Fatalf("URL=%q, want i.js endpoint", params.URL)
		}
		for _, want := range []string{"q=hello+world", "o=json", "u=bing", "vqd=", "bpia=1", "a=h_"} {
			if !strings.Contains(params.URL, want) {
				t.Fatalf("URL %q missing %q", params.URL, want)
			}
		}
		// duckduckgo_extra.py L138-141: safe-search p=1 (level 2).
		if !strings.Contains(params.URL, "p=1") {
			t.Fatalf("URL %q missing safesearch p=1", params.URL)
		}
		if !hasCookie(params.Cookies, "p", "1") || !hasCookie(params.Cookies, "l", "wt-wt") {
			t.Fatalf("cookies missing p/l: %+v", params.Cookies)
		}
	})

	t.Run("videos extra mode", func(t *testing.T) {
		eng := newDuckDuckGo(t, &config.EngineConfig{
			Name: "duckduckgo videos", Engine: "duckduckgo_extra",
			Categories: []string{"videos"},
		})
		params := &engine.RequestParams{Headers: http.Header{}, Pageno: 1}
		if err := eng.Request("rust", params); err != nil {
			t.Fatalf("Request: %v", err)
		}
		if !strings.HasPrefix(params.URL, "https://duckduckgo.com/v.js?") {
			t.Fatalf("URL=%q, want v.js endpoint", params.URL)
		}
	})

	t.Run("news extra mode", func(t *testing.T) {
		eng := newDuckDuckGo(t, &config.EngineConfig{
			Name: "duckduckgo news", Engine: "duckduckgo_extra",
			Categories: []string{"news"},
		})
		params := &engine.RequestParams{Headers: http.Header{}, Pageno: 2, Language: "es"}
		if err := eng.Request("rust", params); err != nil {
			t.Fatalf("Request: %v", err)
		}
		if !strings.HasPrefix(params.URL, "https://duckduckgo.com/news.js?") {
			t.Fatalf("URL=%q, want news.js endpoint", params.URL)
		}
		// duckduckgo_extra.py L135-136: page offset + L132-133: ct=ES.
		if !strings.Contains(params.URL, "s=100") || !strings.Contains(params.URL, "ct=ES") {
			t.Fatalf("URL %q missing s=100/ct=ES", params.URL)
		}
	})

	t.Run("weather mode", func(t *testing.T) {
		eng := newDuckDuckGo(t, &config.EngineConfig{
			Name: "duckduckgo weather", Engine: "duckduckgo_weather",
			Categories: []string{"weather"},
		})
		params := &engine.RequestParams{Headers: http.Header{}}
		if err := eng.Request("paris", params); err != nil {
			t.Fatalf("Request: %v", err)
		}
		// duckduckgo_weather.py L33 + L100: forecast/{query}/{lang} with
		// default lang "en" (eng_lang "en_US" split on "_").
		if params.URL != "https://duckduckgo.com/js/spice/forecast/paris/en" {
			t.Fatalf("URL=%q, want forecast/paris/en", params.URL)
		}
		if !hasCookie(params.Cookies, "ad", "en_US") || !hasCookie(params.Cookies, "l", "wt-wt") {
			t.Fatalf("cookies missing ad/l: %+v", params.Cookies)
		}
		// query is path-escaped: Python quote() -> %20 (L100).
		params2 := &engine.RequestParams{Headers: http.Header{}, Language: "es"}
		if err := eng.Request("new york", params2); err != nil {
			t.Fatalf("Request(spaces): %v", err)
		}
		if params2.URL != "https://duckduckgo.com/js/spice/forecast/new%20york/es" {
			t.Fatalf("URL=%q, want encoded query + lang es", params2.URL)
		}
	})

	t.Run("pageno > 1 without vqd", func(t *testing.T) {
		eng := newDuckDuckGo(t, &config.EngineConfig{
			Name: "duckduckgo", Engine: "duckduckgo", Categories: []string{"general"},
		})
		params := &engine.RequestParams{Headers: http.Header{}, Data: map[string]string{}, Pageno: 2}
		err := eng.Request("rust", params)
		// duckduckgo.py L414-421: SearxEngineCaptchaException(suspended_time=0).
		var susErr *engine.EngineSuspendError
		if !errors.As(err, &susErr) {
			t.Fatalf("Request error %v, want EngineSuspendError", err)
		}
		if susErr.SuspendFor != 0 || !strings.Contains(susErr.Reason, "VQD missed") {
			t.Fatalf("suspend=%+v, want VQD missed with SuspendFor 0", susErr)
		}
	})

	t.Run("query longer than 499 chars clears URL", func(t *testing.T) {
		eng := newDuckDuckGo(t, &config.EngineConfig{
			Name: "duckduckgo", Engine: "duckduckgo", Categories: []string{"general"},
		})
		long := strings.Repeat("x", 500)
		params := &engine.RequestParams{Headers: http.Header{}, Data: map[string]string{}, Pageno: 1}
		if err := eng.Request(long, params); err != nil {
			t.Fatalf("Request: %v", err)
		}
		// duckduckgo.py L364-367: params["url"] = None.
		if params.URL != "" {
			t.Fatalf("URL=%q, want empty (request skipped)", params.URL)
		}
	})

	t.Run("invalid ddg_category is rejected", func(t *testing.T) {
		// duckduckgo_extra.py init() L50-53: ValueError for a bad category.
		_, err := NewDuckDuckGoEngine(&config.EngineConfig{
			Name: "duckduckgo shopping", Engine: "duckduckgo_extra",
			Overrides: map[string]any{"ddg_category": "shopping"},
		})
		if !errors.Is(err, engine.ErrInvalidConfig) {
			t.Fatalf("error %v does not wrap engine.ErrInvalidConfig", err)
		}
	})
}

// TestDuckDuckGoResponseParse covers the HTML no-JS response parsing — port
// of duckduckgo.py response() (L466-519).
func TestDuckDuckGoResponseParse(t *testing.T) {
	eng := newDuckDuckGo(t, &config.EngineConfig{
		Name: "duckduckgo", Engine: "duckduckgo", Categories: []string{"general"},
	})
	fixture := `<html><body>
		<div id="links">
			<div class="web-result">
				<h2><a href="https://example.com/1">First Result</a></h2>
				<a class="result__snippet">The first snippet.</a>
			</div>
			<div class="result--ad result--ad--small">
				<h2><a href="https://ads.example/">Sponsored</a></h2>
			</div>
		</div>
		<div id="zero_click_abstract">42 is the answer <a href="https://example.com/42">source</a></div>
	</body></html>`

	results, err := eng.Response(respBody(fixture))
	if err != nil {
		t.Fatalf("Response: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2 (1 main + 1 answer)", len(results))
	}

	// duckduckgo.py L494-503: web-result divs only; the ad block
	// (result--ad) is ignored.
	m := results[0]
	if m.Kind != result.KindMain {
		t.Fatalf("result[0].Kind=%v, want KindMain", m.Kind)
	}
	if m.Main.Title != "First Result" || m.Main.URL != "https://example.com/1" || m.Main.Content != "The first snippet." {
		t.Fatalf("Main=%+v, want First Result / example.com/1 / snippet", m.Main)
	}

	// duckduckgo.py L505-518: zero-click instant answer.
	a := results[1]
	if a.Kind != result.KindAnswer || len(a.Answer.Answers) != 1 {
		t.Fatalf("result[1]=%+v, want KindAnswer", a)
	}
	if a.Answer.Answers[0].Content != "42 is the answer source" {
		t.Fatalf("answer=%q, want zero-click text", a.Answer.Answers[0].Content)
	}
}

// TestDuckDuckGoResponseParseIgnoresBotNoise verifies the zero-click guards
// of duckduckgo.py L508-512 (debug noise must not become an answer).
func TestDuckDuckGoResponseParseIgnoresBotNoise(t *testing.T) {
	eng := newDuckDuckGo(t, &config.EngineConfig{
		Name: "duckduckgo", Engine: "duckduckgo", Categories: []string{"general"},
	})
	fixture := `<html><body>
		<div id="links"><div class="web-result">
			<h2><a href="https://example.com/">OK</a></h2>
		</div></div>
		<div id="zero_click_abstract">Your IP address is 1.2.3.4</div>
	</body></html>`
	results, err := eng.Response(respBody(fixture))
	if err != nil {
		t.Fatalf("Response: %v", err)
	}
	if len(results) != 1 || results[0].Kind != result.KindMain {
		t.Fatalf("got %d results, want only the main result (no answer)", len(results))
	}
}

// TestDuckDuckGoExtraResponseParse covers the images/videos/news JSON
// parsing — port of duckduckgo_extra.py response() (L187-201).
func TestDuckDuckGoExtraResponseParse(t *testing.T) {
	t.Run("images", func(t *testing.T) {
		eng := newDuckDuckGo(t, &config.EngineConfig{
			Name: "duckduckgo images", Engine: "duckduckgo_extra",
			Categories: []string{"images"}, Overrides: map[string]any{"ddg_category": "images"},
		})
		fixture := `{"results":[
			{"url":"https://example.com/i1","title":"Image One","thumbnail":"https://thumb/1.jpg",
			 "image":"https://img/1.jpg","width":800,"height":600,"source":"Example"},
			{"url":"https://example.com/i2","title":"Image Two"}
		]}`
		results, err := eng.Response(respBody(fixture))
		if err != nil {
			t.Fatalf("Response: %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("got %d results, want 2", len(results))
		}
		// _image_result L150-160: url/title/thumbnail_src/img_src mapped.
		m := results[0].Main
		if m.URL != "https://example.com/i1" || m.Title != "Image One" ||
			m.Thumbnail != "https://thumb/1.jpg" || m.ImgSrc != "https://img/1.jpg" ||
			m.Template != "images.html" {
			t.Fatalf("Main=%+v, want image mapping", m)
		}
		// missing keys default to "" (toString(nil)).
		m2 := results[1].Main
		if m2.ImgSrc != "" || m2.URL != "https://example.com/i2" {
			t.Fatalf("Main=%+v, want empty ImgSrc", m2)
		}
	})

	t.Run("videos", func(t *testing.T) {
		eng := newDuckDuckGo(t, &config.EngineConfig{
			Name: "duckduckgo videos", Engine: "duckduckgo_extra", Categories: []string{"videos"},
		})
		fixture := `{"results":[
			{"title":"Video One","content":"https://youtube.example/watch?v=1",
			 "description":"A video","images":{"small":"https://thumb/1.jpg","medium":"https://thumb/m.jpg"},
			 "provider":"YouTube","duration":"10:00","uploader":"Someone"}
		]}`
		results, err := eng.Response(respBody(fixture))
		if err != nil {
			t.Fatalf("Response: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("got %d results, want 1", len(results))
		}
		// _video_result L163-174: url from content, thumbnail small|medium.
		m := results[0].Main
		if m.URL != "https://youtube.example/watch?v=1" || m.Title != "Video One" ||
			m.Content != "A video" || m.Thumbnail != "https://thumb/1.jpg" || m.Template != "videos.html" {
			t.Fatalf("Main=%+v, want video mapping", m)
		}
	})

	t.Run("news", func(t *testing.T) {
		eng := newDuckDuckGo(t, &config.EngineConfig{
			Name: "duckduckgo news", Engine: "duckduckgo_extra", Categories: []string{"news"},
		})
		fixture := `{"results":[
			{"url":"https://news.example/a","title":"News One",
			 "excerpt":"Breaking <b>news</b> item","source":"Example News","date":1700000000}
		]}`
		results, err := eng.Response(respBody(fixture))
		if err != nil {
			t.Fatalf("Response: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("got %d results, want 1", len(results))
		}
		// _news_result L177-184: excerpt is HTML-stripped.
		m := results[0].Main
		if m.URL != "https://news.example/a" || m.Title != "News One" ||
			m.Content != "Breaking news item" || m.Template != "news.html" {
			t.Fatalf("Main=%+v, want news mapping", m)
		}
	})

	t.Run("missing results key yields nothing", func(t *testing.T) {
		eng := newDuckDuckGo(t, &config.EngineConfig{
			Name: "duckduckgo images", Engine: "duckduckgo_extra",
			Categories: []string{"images"}, Overrides: map[string]any{"ddg_category": "images"},
		})
		results, err := eng.Response(respBody(`{"some":"other"}`))
		if err != nil {
			t.Fatalf("Response: %v", err)
		}
		if len(results) != 0 {
			t.Fatalf("got %d results, want 0", len(results))
		}
	})
}

// TestDuckDuckGoWeatherResponse covers the spice forecast JSONP parsing —
// port of duckduckgo_weather.py response() (L104-126).
func TestDuckDuckGoWeatherResponse(t *testing.T) {
	eng := newDuckDuckGo(t, &config.EngineConfig{
		Name: "duckduckgo weather", Engine: "duckduckgo_weather", Categories: []string{"weather"},
	})

	t.Run("current weather", func(t *testing.T) {
		// The real JSONP envelope ends with a trailing newline after ");",
		// which the Python slice of L110 relies on.
		fixture := "ddg_spice_forecast(\n" +
			`{"currentWeather":{"temperature":21.5,"conditionCode":"Clear","temperatureApparent":20,` +
			`"windDirection":"NW","windSpeed":5,"pressure":1013,"humidity":0.5,"cloudCover":0.1},` +
			`"forecastHourly":{"hours":[]}}` +
			"\n);\n"
		resp := respBody(fixture)
		resp.Request = &http.Request{URL: &url.URL{Path: "/js/spice/forecast/paris/en"}}

		results, err := eng.Response(resp)
		if err != nil {
			t.Fatalf("Response: %v", err)
		}
		if len(results) != 1 || results[0].Kind != result.KindWeather {
			t.Fatalf("got %d results (kind %v), want 1 KindWeather", len(results), results[0].Kind)
		}
		w := results[0].Data.(*result.WeatherAnswer)
		// _weather_data L74-86: temperature °C + WEATHERKIT condition map.
		if w.Temperature != "21.5°C" || w.Condition != "clear sky" {
			t.Fatalf("Weather=%+v, want 21.5°C / clear sky", w)
		}
		if w.Units != "metric" || w.Location != "paris" {
			t.Fatalf("Weather=%+v, want metric / paris", w)
		}
	})

	t.Run("unknown condition code keeps raw value", func(t *testing.T) {
		fixture := "ddg_spice_forecast(\n" +
			`{"currentWeather":{"temperature":10,"conditionCode":"NewWeatherKitCode"},` +
			`"forecastHourly":{"hours":[]}}` +
			"\n);\n"
		results, err := eng.Response(respBody(fixture))
		if err != nil {
			t.Fatalf("Response: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("got %d results, want 1", len(results))
		}
		if w := results[0].Data.(*result.WeatherAnswer); w.Condition != "NewWeatherKitCode" {
			t.Fatalf("Condition=%q, want raw code fallback", w.Condition)
		}
	})

	t.Run("empty jsonp callback", func(t *testing.T) {
		// duckduckgo_weather.py L107-108: no data for the query.
		results, err := eng.Response(respBody("ddg_spice_forecast();"))
		if err != nil {
			t.Fatalf("Response: %v", err)
		}
		if len(results) != 0 {
			t.Fatalf("got %d results, want 0", len(results))
		}
	})
}

// hasCookie reports whether params.Cookies contains a cookie with name and
// value.
func hasCookie(cookies []*http.Cookie, name, value string) bool {
	for _, c := range cookies {
		if c.Name == name && c.Value == value {
			return true
		}
	}
	return false
}
