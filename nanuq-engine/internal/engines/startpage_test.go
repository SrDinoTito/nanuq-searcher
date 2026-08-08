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

// startpageRespBody wraps an inline HTML fixture in a response body, the same
// pattern as bingRespBody.
func startpageRespBody(htmlBody string) *http.Response {
	return &http.Response{Body: io.NopCloser(strings.NewReader(htmlBody))}
}

// mustStartpageEngine builds a Startpage engine for tests.
func mustStartpageEngine(t *testing.T, overrides map[string]any) engine.Engine {
	t.Helper()
	e, err := NewStartpageEngine(&config.EngineConfig{
		Name:      "test",
		Engine:    "startpage",
		Shortcut:  "s",
		Overrides: overrides,
	})
	if err != nil {
		t.Fatalf("NewStartpageEngine: %v", err)
	}
	return e
}

func TestNewStartpageEngineNilConfig(t *testing.T) {
	if _, err := NewStartpageEngine(nil); !errors.Is(err, engine.ErrInvalidConfig) {
		t.Errorf("NewStartpageEngine(nil) error = %v, want ErrInvalidConfig", err)
	}
	if _, err := NewStartpageEngine(&config.EngineConfig{Overrides: map[string]any{"startpage_categ": "video"}}); !errors.Is(err, engine.ErrInvalidConfig) {
		t.Errorf("NewStartpageEngine(invalid categ) error = %v, want ErrInvalidConfig", err)
	}
}

func TestStartpageRequestURLFormat(t *testing.T) {
	e := mustStartpageEngine(t, nil)
	params := &engine.RequestParams{
		Language:   "en-US",
		SafeSearch: 1,
		TimeRange:  "week",
		Pageno:     2,
	}
	if err := e.Request("test query", params); err != nil {
		t.Fatalf("Request: %v", err)
	}

	if params.Method != http.MethodPost {
		t.Errorf("Method = %q, want POST", params.Method)
	}
	if params.URL != "https://www.startpage.com/sp/search" {
		t.Errorf("URL = %q, want https://www.startpage.com/sp/search", params.URL)
	}
	if got := params.Headers.Get("Origin"); got != "https://www.startpage.com" {
		t.Errorf("Origin = %q, want https://www.startpage.com", got)
	}
	if got := params.Headers.Get("Referer"); got != "https://www.startpage.com/" {
		t.Errorf("Referer = %q, want https://www.startpage.com/", got)
	}

	form := map[string]string{
		"query":     "test query",
		"cat":       "web",
		"t":         "device",
		"sc":        "",
		"with_date": "w",
		"abd":       "1",
		"abe":       "1",
		"qsr":       "all",
		"qadf":      "moderate",
		"language":  "en",
		"lui":       "en",
		"page":      "2",
		"segment":   "startpage.udog",
	}
	for key, want := range form {
		if got := params.Data[key]; got != want {
			t.Errorf("Data[%q] = %q, want %q", key, got, want)
		}
	}

	if len(params.Cookies) != 1 {
		t.Fatalf("Cookies = %d, want 1", len(params.Cookies))
	}
	cookie := params.Cookies[0]
	if cookie.Name != "preferences" {
		t.Errorf("Cookie.Name = %q, want preferences", cookie.Name)
	}
	wantCookie := "date_timeEEEworldN1Ndisable_family_filterEEEmoderateN1N" +
		"disable_open_in_new_windowEEE0N1Nenable_post_methodEEE1N1N" +
		"enable_proxy_safety_suggestEEE1N1Nenable_stay_controlEEE1N1N" +
		"instant_answersEEE1N1Nlang_homepageEEEs/device/en/N1N" +
		"num_of_resultsEEE10N1NsuggestionsEEE1N1Nwt_unitEEEcelsiusN1N" +
		"languageEEEenN1Nlanguage_uiEEEen"
	if cookie.Value != wantCookie {
		t.Errorf("Cookie.Value = %q, want %q", cookie.Value, wantCookie)
	}
}

func TestStartpageRequestNilParams(t *testing.T) {
	e := mustStartpageEngine(t, nil)
	if err := e.Request("test query", nil); err == nil {
		t.Error("Request(query, nil) error = nil, want error")
	}
}

func TestStartpageResponseParse(t *testing.T) {
	e := mustStartpageEngine(t, nil)
	body := `<html><body><div id="root">` +
		`React.createElement(UIStartpage.AppSerpWeb, {"render":{"presenter":{"regions":{"mainline":[{` +
		`"results":[` +
		`{"display_type":"web-google","clickUrl":"https://example.com/","title":"Example <b>Title</b>","description":"5 days ago ... Example description"},` +
		`{"display_type":"web-google","clickUrl":"https://example.org/","title":"Second <i>Result</i>","description":"18 Jun 2023 ... Older result"},` +
		`{"display_type":"news-bing","clickUrl":"https://news.example/","title":"<b>News</b> title","description":"News description","date":1687096800000,"thumbnailUrl":"/img/thumb.jpg"},` +
		`{"display_type":"images-bing","altClickUrl":"https://img.example/img.jpg","thumbnailUrl":"/img/t.jpg","width":640,"height":480,"format":"jpeg"},` +
		`{"display_type":"images-bing"}` +
		`]}],"sidebar":[]},"meta":{},"viewId":"serp"},"page":2}})` +
		`</div></body></html>`

	got, err := e.Response(startpageRespBody(body))
	if err != nil {
		t.Fatalf("Response: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("len(results) = %d, want 4", len(got))
	}

	if got[0].Kind != result.KindMain {
		t.Errorf("results[0].Kind = %v, want KindMain", got[0].Kind)
	}
	if got[0].Main.URL != "https://example.com/" {
		t.Errorf("results[0].URL = %q, want https://example.com/", got[0].Main.URL)
	}
	if got[0].Main.Title != "Example Title" {
		t.Errorf("results[0].Title = %q, want Example Title", got[0].Main.Title)
	}
	if got[0].Main.Content != "Example description" {
		t.Errorf("results[0].Content = %q, want Example description", got[0].Main.Content)
	}

	if got[1].Main.URL != "https://example.org/" {
		t.Errorf("results[1].URL = %q, want https://example.org/", got[1].Main.URL)
	}
	if got[1].Main.Content != "Older result" {
		t.Errorf("results[1].Content = %q, want Older result", got[1].Main.Content)
	}

	if got[2].Main.URL != "https://news.example/" {
		t.Errorf("results[2].URL = %q, want https://news.example/", got[2].Main.URL)
	}
	if got[2].Main.Thumbnail != "https://www.startpage.com/img/thumb.jpg" {
		t.Errorf("results[2].Thumbnail = %q, want https://www.startpage.com/img/thumb.jpg", got[2].Main.Thumbnail)
	}

	if got[3].Kind != result.KindImage {
		t.Fatalf("results[3].Kind = %v, want KindImage", got[3].Kind)
	}
	img, ok := got[3].Data.(*result.Image)
	if !ok {
		t.Fatalf("results[3].Data type = %T, want *result.Image", got[3].Data)
	}
	if img.ThumbnailSrc != "https://www.startpage.com/img/t.jpg" {
		t.Errorf("img.ThumbnailSrc = %q, want https://www.startpage.com/img/t.jpg", img.ThumbnailSrc)
	}
	if img.Resolution != "640x480" {
		t.Errorf("img.Resolution = %q, want 640x480", img.Resolution)
	}
	if img.ImgFormat != "JPEG" {
		t.Errorf("img.ImgFormat = %q, want JPEG", img.ImgFormat)
	}
}

func TestStartpageResponseNoResults(t *testing.T) {
	e := mustStartpageEngine(t, nil)
	body := `<html><body><div id="root">` +
		`React.createElement(UIStartpage.AppSerpWeb, {"render":{"presenter":{"regions":{"mainline":[]},"meta":{},"viewId":"serp"},"page":1}})` +
		`</div></body></html>`

	got, err := e.Response(startpageRespBody(body))
	if err != nil {
		t.Fatalf("Response: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len(results) = %d, want 0", len(got))
	}
}

func TestStartpageResponseCaptcha(t *testing.T) {
	e := mustStartpageEngine(t, nil)
	resp := startpageRespBody(`<html>blocked</html>`)
	resp.Header = http.Header{}
	resp.Header.Set("Location", "https://www.startpage.com/sp/captcha?x=1")

	if _, err := e.Response(resp); err == nil {
		t.Fatal("Response error = nil, want EngineSuspendError")
	} else {
		var se *engine.EngineSuspendError
		if !errors.As(err, &se) {
			t.Errorf("Response error type = %T, want *engine.EngineSuspendError", err)
		}
	}
}

func TestStartpageParsePublishedDate(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		wantContent string
	}{
		{"days ago", "5 days ago ... Example description", "Example description"},
		{"day ago", "1 day ago ... Single day", "Single day"},
		{"date prefix", "18 Jun 2023 ... Older result", "Older result"},
		{"no date", "Plain content", "Plain content"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content, _ := startpageParsePublishedDate(tc.input)
			if content != tc.wantContent {
				t.Errorf("content = %q, want %q", content, tc.wantContent)
			}
		})
	}
}
