// This file is a faithful Go port of SearXNG's searx/engines/bing_news.py.
// It reuses the shared helpers from bing.go (bingBase, bingRegion,
// bingOverrideAcceptLanguage, bingLocaleParams).
package engines

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/antchfx/htmlquery"
	"golang.org/x/net/html"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
	"nanuq-engine/internal/result"
)

// bingNewsTimeMap ports bing_news.py's time_map (L38-42): time range name to
// the qft filterui interval. Per the Python comment, "day" maps to the last
// hour ("interval=\"4\"") and Bing has no "year" (month is used instead).
var bingNewsTimeMap = map[string]string{
	"day":   `interval="4"`,
	"week":  `interval="7"`,
	"month": `interval="9"`,
}

// bingNewsEngine implements engine.Engine for Bing news results
// (bing_news.py).
type bingNewsEngine struct {
	bingBase
}

// NewBingNewsEngine builds one Bing News engine per YAML entry.
func NewBingNewsEngine(cfg *config.EngineConfig) (engine.Engine, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%w: bing news engine: nil config", engine.ErrInvalidConfig)
	}
	return &bingNewsEngine{
		bingBase: bingBase{
			cfg:     cfg,
			baseURL: overrideStringDef(cfg.Overrides, "base_url", "https://www.bing.com"),
			defCats: []string{"news"},
		},
	}, nil
}

// Request mutates params to build the Bing News infinitescrollajax request.
// Port of bing_news.py request() (L51-77).
func (e *bingNewsEngine) Request(query string, params *engine.RequestParams) error {
	if params == nil {
		return errors.New("bing news engine: nil request params")
	}

	// bing_news.py L51-52: region resolution + Accept-Language override.
	region := bingRegion(params)
	bingOverrideAcceptLanguage(params, region)

	// bing_news.py L53: page = int(params.get("pageno", 1)) - 1 (0-based).
	pageno := params.Pageno
	if pageno < 1 {
		pageno = 1
	}
	page := pageno - 1

	// bing_news.py L54-60: query_params = {"q": query, "InfiniteScroll": 1,
	// "first": page * 10 + 1, "SFX": page, "form": "PTFTNR"}.
	v := url.Values{}
	v.Set("q", query)
	v.Set("InfiniteScroll", "1")
	v.Set("first", strconv.Itoa(page*10+1))
	v.Set("SFX", strconv.Itoa(page))
	v.Set("form", "PTFTNR")

	// bing_news.py L61-63: merge get_locale_params(engine_region) ("mkt").
	if mkt := bingLocaleParams(region); mkt != "" {
		v.Set("mkt", mkt)
	}

	// bing_news.py L65-68: qft = time_map.get(time_range, 'interval="9"')
	// (map .get with a default, unlike bing_images' direct key access).
	if params.TimeRange != "" {
		qft, ok := bingNewsTimeMap[params.TimeRange]
		if !ok {
			qft = `interval="9"`
		}
		v.Set("qft", qft)
	}

	// bing_news.py L76-77: params["url"] = base_url + "/news/infinitescrollajax?" +
	// urlencode(query_params).
	params.URL = e.baseURL + "/news/infinitescrollajax?" + v.Encode()
	params.Method = http.MethodGet
	return nil
}

// Response parses the Bing News infinitescrollajax HTML and extracts the news
// results. Port of bing_news.py response() (L80-127).
func (e *bingNewsEngine) Response(resp *http.Response) ([]*result.RawResult, error) {
	if resp == nil {
		return nil, errors.New("bing news engine: nil http response")
	}

	doc, err := htmlquery.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("bing news engine %q: parse response body: %w", e.cfg.Name, err)
	}

	results := []*result.RawResult{}

	// bing_news.py L82-83: for newsitem in eval_xpath_list(dom,
	// '//div[contains(@class, "newsitem")]').
	items, err := htmlquery.QueryAll(doc, `//div[contains(@class, "newsitem")]`)
	if err != nil {
		return nil, fmt.Errorf("bing news engine %q: evaluate result xpath: %w", e.cfg.Name, err)
	}
	for _, item := range items {
		// bing_news.py L84-86: link = eval_xpath_getindex(newsitem,
		// './/a[@class="title"]', 0, None); if link is None: continue.
		link := bingQuery(item, `.//a[@class="title"]`)
		if link == nil {
			continue
		}

		// bing_news.py L87-88: url = link.attrib.get("href"); title = extract_text(link).
		newsURL := htmlquery.SelectAttr(link, "href")
		title := extractText([]*html.Node{link})

		// bing_news.py L89: content = extract_text(newsitem './/div[@class="snippet"]').
		content := extractTextFromNode(item, `.//div[@class="snippet"]`)

		// bing_news.py L91-102 builds the "metadata" string from the source
		// aria-label and the link's data-author ("source | author"). The Go
		// result.MainResult model has no metadata field, so it is dropped here
		// (deviation, DSG-014).

		// bing_news.py L103-110: thumbnail from './/a[@class="imagelink"]//img'
		// @src, prefixed with the base URL when relative.
		thumbnail := ""
		if img := bingQuery(item, `.//a[@class="imagelink"]//img`); img != nil {
			thumbnail = htmlquery.SelectAttr(img, "src")
			if thumbnail != "" && !strings.HasPrefix(thumbnail, "https://www.bing.com") {
				thumbnail = "https://www.bing.com/" + thumbnail
			}
		}

		// bing_news.py L124-126: append {"url", "title", "content",
		// "thumbnail", "metadata"}.
		results = append(results, result.NewMain(&result.MainResult{
			URL:       newsURL,
			Title:     title,
			Content:   content,
			Thumbnail: thumbnail,
		}))
	}

	slog.Debug("bing news engine: extracted results", "engine", e.cfg.Name, "count", len(results))
	return results, nil
}
