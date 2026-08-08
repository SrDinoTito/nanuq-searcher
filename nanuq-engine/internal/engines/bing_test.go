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

// bingRespBody wraps an HTML body into a synthetic *http.Response, mirroring
// xpathRespBody in xpath_test.go.
func bingRespBody(htmlBody string) *http.Response {
	return &http.Response{Body: io.NopCloser(strings.NewReader(htmlBody))}
}

// mustBingEngine builds a bing-family engine with the given constructor and
// overrides, failing the test on error.
func mustBingEngine(t *testing.T, newFn func(*config.EngineConfig) (engine.Engine, error), overrides map[string]any) engine.Engine {
	t.Helper()
	e, err := newFn(&config.EngineConfig{
		Name:      "test",
		Engine:    "bing",
		Shortcut:  "b",
		Overrides: overrides,
	})
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}
	return e
}

// TestBingRequestURLFormat checks the request URL and Accept-Language header
// with a region and moderate safesearch (bing.py L74-93, L96-112).
func TestBingRequestURLFormat(t *testing.T) {
	e := mustBingEngine(t, NewBingEngine, nil)
	params := &engine.RequestParams{Language: "en-US", SafeSearch: 1}
	if err := e.Request("test", params); err != nil {
		t.Fatalf("Request returned error: %v", err)
	}
	wantURL := "https://www.bing.com/search?adlt=moderate&mkt=en-US&q=test"
	if params.URL != wantURL {
		t.Errorf("URL = %q, want %q", params.URL, wantURL)
	}
	if params.Method != http.MethodGet {
		t.Errorf("Method = %q, want GET", params.Method)
	}
	got := params.Headers.Get("Accept-Language")
	if want := "en-US,en;q=0.9"; got != want {
		t.Errorf("Accept-Language = %q, want %q", got, want)
	}
}

// TestBingRequestNoRegion checks that no region means no mkt parameter and no
// Accept-Language override, with default safesearch "off" (bing.py L54-71,
// L74-93).
func TestBingRequestNoRegion(t *testing.T) {
	e := mustBingEngine(t, NewBingEngine, nil)
	params := &engine.RequestParams{}
	if err := e.Request("test", params); err != nil {
		t.Fatalf("Request returned error: %v", err)
	}
	wantURL := "https://www.bing.com/search?adlt=off&q=test"
	if params.URL != wantURL {
		t.Errorf("URL = %q, want %q", params.URL, wantURL)
	}
	if got := params.Headers.Get("Accept-Language"); got != "" {
		t.Errorf("Accept-Language = %q, want empty", got)
	}
}

// TestBingResponseParse checks response parsing: ck/a redirect decoding,
// algoSlug_icon removal and skipping of results without a link (bing.py
// L115-155).
func TestBingResponseParse(t *testing.T) {
	body := `<!DOCTYPE html>
<html>
<body>
<ol id="b_results">
  <li class="b_algo">
    <h2><a href="https://www.bing.com/ck/a?u=a1aHR0cHM6Ly9leGFtcGxlLm9yZy9yZWFsLXJlc3VsdA==">Redirected result</a></h2>
    <p>first snippet</p>
  </li>
  <li class="b_algo">
    <h2><a href="https://example.org/direct">Direct result</a></h2>
    <p>with <span class="algoSlug_icon">icon-to-remove</span> here</p>
  </li>
  <li class="b_algo">
    <h2><a href="">No href</a></h2>
  </li>
</ol>
</body>
</html>`
	e := mustBingEngine(t, NewBingEngine, nil)
	got, err := e.Response(bingRespBody(body))
	if err != nil {
		t.Fatalf("Response returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
	if got[0].Kind != result.KindMain {
		t.Fatalf("result 0 Kind = %v, want KindMain", got[0].Kind)
	}
	if got[0].Main.URL != "https://example.org/real-result" {
		t.Errorf("result 0 URL = %q, want decoded ck/a URL", got[0].Main.URL)
	}
	if got[0].Main.Title != "Redirected result" {
		t.Errorf("result 0 Title = %q", got[0].Main.Title)
	}
	if got[0].Main.Content != "first snippet" {
		t.Errorf("result 0 Content = %q, want %q", got[0].Main.Content, "first snippet")
	}
	if got[1].Main.URL != "https://example.org/direct" {
		t.Errorf("result 1 URL = %q", got[1].Main.URL)
	}
	if got[1].Main.Content != "with here" {
		t.Errorf("result 1 Content = %q, want %q (algoSlug_icon removed)", got[1].Main.Content, "with here")
	}
}

// TestBingImagesRequestURLFormat checks the images async request with paging
// and time range (bing_images.py L41-67).
func TestBingImagesRequestURLFormat(t *testing.T) {
	e := mustBingEngine(t, NewBingImagesEngine, nil)
	params := &engine.RequestParams{Language: "es-ES", Pageno: 2, TimeRange: "week"}
	if err := e.Request("gato", params); err != nil {
		t.Fatalf("Request returned error: %v", err)
	}
	wantURL := "https://www.bing.com/images/async?async=1&count=35&first=36&mkt=es-ES&q=gato&qft=filterui%3Aage-lt10080"
	if params.URL != wantURL {
		t.Errorf("URL = %q, want %q", params.URL, wantURL)
	}
}

// TestBingImagesResponseParse checks image extraction: @m JSON metadata,
// resolution/format from the span text (bing_images.py L70-99).
func TestBingImagesResponseParse(t *testing.T) {
	body := `<!DOCTYPE html>
<html>
<body>
<ul class="dgControl_list">
  <li>
    <a class="iusc" m='{"purl":"https://example.org/pic","turl":"https://thumb.example/1.jpg","murl":"https://media.example/1.jpg","desc":"a cat"}'>x</a>
    <div class="infnmpt"><a>Cat photo</a></div>
    <div class="imgpt"><div><span>1920 × 1080 · JPEG</span></div></div>
  </li>
</ul>
</body>
</html>`
	e := mustBingEngine(t, NewBingImagesEngine, nil)
	got, err := e.Response(bingRespBody(body))
	if err != nil {
		t.Fatalf("Response returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d results, want 1", len(got))
	}
	if got[0].Kind != result.KindImage {
		t.Fatalf("Kind = %v, want KindImage", got[0].Kind)
	}
	img, ok := got[0].Data.(*result.Image)
	if !ok {
		t.Fatalf("Data = %T, want *result.Image", got[0].Data)
	}
	if img.ThumbnailSrc != "https://thumb.example/1.jpg" {
		t.Errorf("ThumbnailSrc = %q, want turl", img.ThumbnailSrc)
	}
	if img.Resolution != "1920 × 1080" {
		t.Errorf("Resolution = %q, want %q", img.Resolution, "1920 × 1080")
	}
	if img.ImgFormat != "JPEG" {
		t.Errorf("ImgFormat = %q, want %q", img.ImgFormat, "JPEG")
	}
}

// TestBingNewsRequestURLFormat checks the news infinitescrollajax request with
// paging and time range (bing_news.py L51-77).
func TestBingNewsRequestURLFormat(t *testing.T) {
	e := mustBingEngine(t, NewBingNewsEngine, nil)
	params := &engine.RequestParams{Language: "en-US", Pageno: 3, TimeRange: "week"}
	if err := e.Request("london", params); err != nil {
		t.Fatalf("Request returned error: %v", err)
	}
	wantURL := "https://www.bing.com/news/infinitescrollajax?InfiniteScroll=1&SFX=2&first=21&form=PTFTNR&mkt=en-US&q=london&qft=interval%3D%227%22"
	if params.URL != wantURL {
		t.Errorf("URL = %q, want %q", params.URL, wantURL)
	}
}

// TestBingNewsResponseParse checks news extraction: title, snippet, thumbnail
// prefixing and skipping items without a title link (bing_news.py L80-127).
func TestBingNewsResponseParse(t *testing.T) {
	body := `<!DOCTYPE html>
<html>
<body>
<div class="newsitem">
  <a class="title" href="https://news.example/1" data-author="Jane Doe">First story</a>
  <div class="snippet">Snippet text</div>
  <div class="source"><span aria-label="Reuters">Reuters</span></div>
  <a class="imagelink"><img src="thumbs/thumb1.jpg" /></a>
</div>
<div class="newsitem">
  <a class="title" href="https://news.example/2">Second story</a>
  <div class="snippet">Another snippet</div>
</div>
<div class="newsitem">
  <div class="snippet">No title link here</div>
</div>
</body>
</html>`
	e := mustBingEngine(t, NewBingNewsEngine, nil)
	got, err := e.Response(bingRespBody(body))
	if err != nil {
		t.Fatalf("Response returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
	if got[0].Main.URL != "https://news.example/1" {
		t.Errorf("result 0 URL = %q", got[0].Main.URL)
	}
	if got[0].Main.Title != "First story" {
		t.Errorf("result 0 Title = %q", got[0].Main.Title)
	}
	if got[0].Main.Content != "Snippet text" {
		t.Errorf("result 0 Content = %q", got[0].Main.Content)
	}
	if got[0].Main.Thumbnail != "https://www.bing.com/thumbs/thumb1.jpg" {
		t.Errorf("result 0 Thumbnail = %q, want base-prefixed", got[0].Main.Thumbnail)
	}
	if got[1].Main.Thumbnail != "" {
		t.Errorf("result 1 Thumbnail = %q, want empty", got[1].Main.Thumbnail)
	}
}

// TestBingVideosRequestURLFormat checks the videos asyncv2 request with paging
// and time range, including the leading space in the qft value (bing_videos.py
// L36-65).
func TestBingVideosRequestURLFormat(t *testing.T) {
	e := mustBingEngine(t, NewBingVideosEngine, nil)
	params := &engine.RequestParams{Language: "en-US", Pageno: 2, TimeRange: "week"}
	if err := e.Request("penguin", params); err != nil {
		t.Fatalf("Request returned error: %v", err)
	}
	wantURL := "https://www.bing.com/videos/asyncv2?async=content&count=35&first=36&form=VRFLTR&mkt=en-US&q=penguin&qft=+filterui%3Avideoage-lt10080"
	if params.URL != wantURL {
		t.Errorf("URL = %q, want %q", params.URL, wantURL)
	}
}

// TestBingVideosResponseParse checks video extraction: @vrhm JSON metadata,
// meta block spans joined with " - ", and the rms thumbnail (bing_videos.py
// L68-96).
func TestBingVideosResponseParse(t *testing.T) {
	body := `<!DOCTYPE html>
<html>
<body>
<div id="mc_vtvc_video1">
  <div class="vrhdata" vrhm='{"murl":"https://media.example/v1.mp4","vt":"Penguin doc","du":"PT1H2M3S"}'></div>
  <div class="mc_vtvc_meta_block"><span>YouTube</span><span>2.3M views</span><span>1:02:03</span></div>
  <img class="rms-thumb" data-src-hq="https://thumb.example/v1.jpg" />
</div>
</body>
</html>`
	e := mustBingEngine(t, NewBingVideosEngine, nil)
	got, err := e.Response(bingRespBody(body))
	if err != nil {
		t.Fatalf("Response returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d results, want 1", len(got))
	}
	if got[0].Main.URL != "https://media.example/v1.mp4" {
		t.Errorf("URL = %q, want murl", got[0].Main.URL)
	}
	if got[0].Main.Title != "Penguin doc" {
		t.Errorf("Title = %q, want vt", got[0].Main.Title)
	}
	if got[0].Main.Thumbnail != "https://thumb.example/v1.jpg" {
		t.Errorf("Thumbnail = %q, want data-src-hq", got[0].Main.Thumbnail)
	}
	if got[0].Main.Content != "YouTube - 2.3M views - 1:02:03" {
		t.Errorf("Content = %q, want %q", got[0].Main.Content, "YouTube - 2.3M views - 1:02:03")
	}
}

func TestNewBingEngineNilConfig(t *testing.T) {
	if _, err := NewBingEngine(nil); !errors.Is(err, engine.ErrInvalidConfig) {
		t.Errorf("err = %v, want ErrInvalidConfig wrap", err)
	}
}

func TestNewBingImagesEngineNilConfig(t *testing.T) {
	if _, err := NewBingImagesEngine(nil); !errors.Is(err, engine.ErrInvalidConfig) {
		t.Errorf("err = %v, want ErrInvalidConfig wrap", err)
	}
}

func TestNewBingNewsEngineNilConfig(t *testing.T) {
	if _, err := NewBingNewsEngine(nil); !errors.Is(err, engine.ErrInvalidConfig) {
		t.Errorf("err = %v, want ErrInvalidConfig wrap", err)
	}
}

func TestNewBingVideosEngineNilConfig(t *testing.T) {
	if _, err := NewBingVideosEngine(nil); !errors.Is(err, engine.ErrInvalidConfig) {
		t.Errorf("err = %v, want ErrInvalidConfig wrap", err)
	}
}
