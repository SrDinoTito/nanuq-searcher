// This file is a faithful Go port of SearXNG's searx/engines/bing_images.py.
// It reuses the shared helpers from bing.go (bingBase, bingRegion,
// bingOverrideAcceptLanguage, bingLocaleParams).
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

// bingImagesTimeMap ports bing_images.py's time_map (L30-35): time range name
// to "minutes ago" values used by the qft parameter. bing_videos.py imports
// the same table, so it is declared once here.
var bingImagesTimeMap = map[string]int{
	"day":   60 * 24,
	"week":  60 * 24 * 7,
	"month": 60 * 24 * 31,
	"year":  60 * 24 * 365,
}

// bingImageMeta is the JSON payload carried by the @m attribute of each
// <a class="iusc"> element (bing_images.py L74-78): purl (page URL), turl
// (thumbnail URL), murl (full image URL) and desc (image description).
type bingImageMeta struct {
	PURL string `json:"purl"`
	TURL string `json:"turl"`
	MURL string `json:"murl"`
	Desc string `json:"desc"`
}

// bingImagesEngine implements engine.Engine for Bing image results
// (bing_images.py).
type bingImagesEngine struct {
	bingBase
}

// NewBingImagesEngine builds one Bing Images engine per YAML entry.
func NewBingImagesEngine(cfg *config.EngineConfig) (engine.Engine, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%w: bing images engine: nil config", engine.ErrInvalidConfig)
	}
	return &bingImagesEngine{
		bingBase: bingBase{
			cfg:     cfg,
			baseURL: overrideStringDef(cfg.Overrides, "base_url", "https://www.bing.com"),
			defCats: []string{"images", "web"},
		},
	}, nil
}

// Request mutates params to build the Bing Images async request. Port of
// bing_images.py request() (L41-67).
func (e *bingImagesEngine) Request(query string, params *engine.RequestParams) error {
	if params == nil {
		return errors.New("bing images engine: nil request params")
	}

	// bing_images.py L41-42: region resolution + Accept-Language override.
	region := bingRegion(params)
	bingOverrideAcceptLanguage(params, region)

	// bing_images.py L43-45: pageno default 1 (int(params.get("pageno", 1))).
	pageno := params.Pageno
	if pageno < 1 {
		pageno = 1
	}

	// bing_images.py L46-51: query_params = {"q": query, "async": "1",
	// "first": (pageno - 1) * 35 + 1, "count": 35}.
	v := url.Values{}
	v.Set("q", query)
	v.Set("async", "1")
	v.Set("first", strconv.Itoa((pageno-1)*35+1))
	v.Set("count", "35")

	// bing_images.py L52-54: merge get_locale_params(engine_region) ("mkt").
	if mkt := bingLocaleParams(region); mkt != "" {
		v.Set("mkt", mkt)
	}

	// bing_images.py L56-58: qft = "filterui:age-lt%s" % time_map[time_range].
	// Python uses direct key access (KeyError on unknown ranges); Go skips the
	// parameter instead (defensive, EC-011).
	if params.TimeRange != "" {
		if mins, ok := bingImagesTimeMap[params.TimeRange]; ok {
			v.Set("qft", fmt.Sprintf("filterui:age-lt%d", mins))
		}
	}

	// bing_images.py L66-67: params["url"] = base_url + "/images/async?" +
	// urlencode(query_params).
	params.URL = e.baseURL + "/images/async?" + v.Encode()
	params.Method = http.MethodGet
	return nil
}

// Response parses the Bing Images async HTML and extracts the image results.
// Port of bing_images.py response() (L70-99).
func (e *bingImagesEngine) Response(resp *http.Response) ([]*result.RawResult, error) {
	if resp == nil {
		return nil, errors.New("bing images engine: nil http response")
	}

	doc, err := htmlquery.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("bing images engine %q: parse response body: %w", e.cfg.Name, err)
	}

	results := []*result.RawResult{}

	// bing_images.py L72: for result in dom.xpath(
	// '//ul[contains(@class, "dgControl_list")]/li').
	items, err := htmlquery.QueryAll(doc, `//ul[contains(@class, "dgControl_list")]/li`)
	if err != nil {
		return nil, fmt.Errorf("bing images engine %q: evaluate result xpath: %w", e.cfg.Name, err)
	}
	for _, item := range items {
		// bing_images.py L74-77: metadata = result.xpath('.//a[@class="iusc"]/@m');
		// if not metadata: continue.
		mNodes := bingQueryAll(item, `.//a[@class="iusc"]/@m`)
		if len(mNodes) == 0 {
			continue
		}
		// bing_images.py L78: metadata = json.loads(metadata[0]).
		var m bingImageMeta
		if err := json.Unmarshal([]byte(htmlquery.InnerText(mNodes[0])), &m); err != nil {
			// Malformed metadata: Python raises; skip the item instead
			// (defensive, EC-011).
			continue
		}

		// bing_images.py L79-81: title = " ".join(... './/div[@class="infnmpt"]//a/text()').strip().
		// The title has no field in result.Image, so it is extracted only to
		// mirror the Python and then discarded (see drop note below).
		_ = extractTextFromNode(item, `.//div[@class="infnmpt"]//a/text()`)

		// bing_images.py L82-84: img_format = " ".join(... './/div[@class="imgpt"]/div/span/text()').strip().split(" · ")
		// The separator is U+00B7 MIDDLE DOT surrounded by spaces.
		imgFormat := extractTextFromNode(item, `.//div[@class="imgpt"]/div/span/text()`)
		parts := strings.Split(imgFormat, " · ")
		resolution := ""
		if len(parts) > 0 {
			resolution = parts[0]
		}
		imgFormatPart := ""
		if len(parts) >= 2 {
			imgFormatPart = parts[1]
		}

		// bing_images.py L87-91 builds a full images.html result (purl, turl,
		// murl, desc, title, source). The Go result.Image model only carries
		// ThumbnailSrc/Resolution/ImgFormat, so purl/murl/desc/title/source are
		// dropped here (deviation, DSG-014). The container additionally skips
		// KindImage results for now (TODO TASK-006 phase B).
		results = append(results, result.NewImage(&result.Image{
			ThumbnailSrc: m.TURL,
			Resolution:   resolution,
			ImgFormat:    imgFormatPart,
		}))
	}

	slog.Debug("bing images engine: extracted results", "engine", e.cfg.Name, "count", len(results))
	return results, nil
}
