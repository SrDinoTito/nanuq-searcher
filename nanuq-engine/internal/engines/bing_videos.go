// This file is a faithful Go port of SearXNG's searx/engines/bing_videos.py.
// It reuses the shared helpers from bing.go (bingBase, bingRegion,
// bingOverrideAcceptLanguage, bingLocaleParams) and bing_images.go
// (bingImagesTimeMap).
package engines

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/antchfx/htmlquery"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
	"nanuq-engine/internal/result"
)

// bingVideoMeta is the JSON payload carried by the @vrhm attribute of each
// video item (bing_videos.py L69-70): murl (media URL), vt (title) and du
// (duration).
type bingVideoMeta struct {
	MURL string `json:"murl"`
	VT   string `json:"vt"`
	DU   string `json:"du"`
}

// bingVideosEngine implements engine.Engine for Bing video results
// (bing_videos.py).
type bingVideosEngine struct {
	bingBase
}

// NewBingVideosEngine builds one Bing Videos engine per YAML entry.
func NewBingVideosEngine(cfg *config.EngineConfig) (engine.Engine, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%w: bing videos engine: nil config", engine.ErrInvalidConfig)
	}
	return &bingVideosEngine{
		bingBase: bingBase{
			cfg:     cfg,
			baseURL: overrideStringDef(cfg.Overrides, "base_url", "https://www.bing.com"),
			defCats: []string{"videos", "web"},
		},
	}, nil
}

// Request mutates params to build the Bing Videos asyncv2 request. Port of
// bing_videos.py request() (L36-65). The time_map table comes from
// bing_images.py (bing_videos.py L32 imports it).
func (e *bingVideosEngine) Request(query string, params *engine.RequestParams) error {
	if params == nil {
		return errors.New("bing videos engine: nil request params")
	}

	// bing_videos.py L36-37: region resolution + Accept-Language override.
	region := bingRegion(params)
	bingOverrideAcceptLanguage(params, region)

	// bing_videos.py L38-40: pageno default 1 (int(params.get("pageno", 1))).
	pageno := params.Pageno
	if pageno < 1 {
		pageno = 1
	}

	// bing_videos.py L41-45: query_params = {"q": query, "async": "content",
	// "first": (pageno - 1) * 35 + 1, "count": 35}.
	v := url.Values{}
	v.Set("q", query)
	v.Set("async", "content")
	v.Set("first", strconv.Itoa((pageno-1)*35+1))
	v.Set("count", "35")

	// bing_videos.py L46-48: merge get_locale_params(engine_region) ("mkt").
	if mkt := bingLocaleParams(region); mkt != "" {
		v.Set("mkt", mkt)
	}

	// bing_videos.py L50-54: if params["time_range"]: form = "VRFLTR" and
	// qft = " filterui:videoage-lt%s" % time_map[time_range]. The leading
	// space in the qft value is kept faithfully. Python uses direct key access
	// (KeyError on unknown ranges); Go skips the parameter instead
	// (defensive, EC-011).
	if params.TimeRange != "" {
		if mins, ok := bingImagesTimeMap[params.TimeRange]; ok {
			v.Set("form", "VRFLTR")
			v.Set("qft", fmt.Sprintf(" filterui:videoage-lt%d", mins))
		}
	}

	// bing_videos.py L64-65: params["url"] = base_url + "/videos/asyncv2?" +
	// urlencode(query_params).
	params.URL = e.baseURL + "/videos/asyncv2?" + v.Encode()
	params.Method = http.MethodGet
	return nil
}

// Response parses the Bing Videos asyncv2 HTML and extracts the video
// results. Port of bing_videos.py response() (L68-96).
func (e *bingVideosEngine) Response(resp *http.Response) ([]*result.RawResult, error) {
	if resp == nil {
		return nil, errors.New("bing videos engine: nil http response")
	}

	doc, err := htmlquery.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("bing videos engine %q: parse response body: %w", e.cfg.Name, err)
	}

	results := []*result.RawResult{}

	// bing_videos.py L70: for result in dom.xpath(
	// '//div[contains(@id, "mc_vtvc_video")]').
	items, err := htmlquery.QueryAll(doc, `//div[contains(@id, "mc_vtvc_video")]`)
	if err != nil {
		return nil, fmt.Errorf("bing videos engine %q: evaluate result xpath: %w", e.cfg.Name, err)
	}
	for _, item := range items {
		// bing_videos.py L71: metadata = json.loads(eval_xpath_getindex(result,
		// './/div[@class="vrhdata"]/@vrhm', index=0)).
		vrhmNodes := bingQueryAll(item, `.//div[@class="vrhdata"]/@vrhm`)
		if len(vrhmNodes) == 0 {
			continue
		}
		var m bingVideoMeta
		if err := json.Unmarshal([]byte(htmlquery.InnerText(vrhmNodes[0])), &m); err != nil {
			// Malformed metadata: Python raises; skip the item instead
			// (defensive, EC-011).
			continue
		}

		// bing_videos.py L72-73: info = " - ".join(
		// result './/div[@class="mc_vtvc_meta_block"]//span/text()').strip().
		spanNodes := bingQueryAll(item, `.//div[@class="mc_vtvc_meta_block"]//span/text()`)
		infoParts := make([]string, 0, len(spanNodes))
		for _, n := range spanNodes {
			infoParts = append(infoParts, htmlquery.InnerText(n))
		}
		info := strings.TrimSpace(strings.Join(infoParts, " - "))

		// bing_videos.py L74-75: thumbnail = eval_xpath_getindex(result,
		// './/img[starts-with(@class, "rms")]/@data-src-hq', 0, None).
		thumbnail := ""
		if tn := bingQueryAll(item, `.//img[starts-with(@class, "rms")]/@data-src-hq`); len(tn) > 0 {
			thumbnail = htmlquery.InnerText(tn[0])
		}

		// bing_videos.py L89-94 builds a videos.html result including "length"
		// (metadata["du"]). The Go result.MainResult model has no length field,
		// so it is dropped here (deviation, DSG-014).

		results = append(results, result.NewMain(&result.MainResult{
			URL:       m.MURL,
			Thumbnail: thumbnail,
			Title:     m.VT,
			Content:   info,
		}))
	}

	slog.Debug("bing videos engine: extracted results", "engine", e.cfg.Name, "count", len(results))
	return results, nil
}
