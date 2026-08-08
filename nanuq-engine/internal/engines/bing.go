// Package engines implements the engine modules of nanuq-engine.
//
// This file is a faithful Go port of SearXNG's searx/engines/bing.py (web
// results). The companion modules bing_images.go, bing_news.go and
// bing_videos.go port bing_images.py, bing_news.py and bing_videos.py and
// share the region / Accept-Language helpers declared here.
package engines

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/antchfx/htmlquery"
	"golang.org/x/net/html"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
	"nanuq-engine/internal/result"
)

// bingSafeSearchMap ports bing.py's _safesearch_map (L43-47): the adlt query
// parameter values for safe search off / moderate / strict.
var bingSafeSearchMap = map[int]string{
	0: "off",
	1: "moderate",
	2: "strict",
}

// bingBase holds the state shared by all four Bing modules (web, images, news
// and videos): the owning YAML entry plus the module defaults read at
// construction. One instance per YAML engine entry (1:N pattern).
type bingBase struct {
	cfg *config.EngineConfig

	// baseURL ports the base_url module attribute (bing.py L50). SearXNG
	// settings may override module attributes, so it is read from
	// cfg.Overrides["base_url"] with the Python default as fallback.
	baseURL string

	// defCats are the module's categories (e.g. bing.py L41); they are used
	// when the YAML entry does not declare categories of its own.
	defCats []string
}

// Name returns the engine's configured name (YAML entry name).
func (b *bingBase) Name() string { return b.cfg.Name }

// Shortcut returns the configured shortcut.
func (b *bingBase) Shortcut() string { return b.cfg.Shortcut }

// Categories returns the configured categories, falling back to the module's
// default categories when the YAML entry declares none.
func (b *bingBase) Categories() []string {
	if len(b.cfg.Categories) > 0 {
		return b.cfg.Categories
	}
	return b.defCats
}

// NeedsInit reports that no per-engine init is required.
func (b *bingBase) NeedsInit() bool { return false }

// Setup is a no-op for the Bing engines (all config comes from Overrides).
func (b *bingBase) Setup(_ context.Context, _ *config.EngineConfig) error { return nil }

// Init is a no-op for the Bing engines. The Python modules fetch a traits
// table at init (bing.py L158-210 fetch_traits); that table is out of scope
// for this port — the search processor supplies the region directly via
// params.Language (see bingRegion).
func (b *bingBase) Init(_ context.Context) error { return nil }

// bingEngine implements engine.Engine for Bing web results (bing.py).
type bingEngine struct {
	bingBase
}

// NewBingEngine builds one Bing (web) engine per YAML entry.
func NewBingEngine(cfg *config.EngineConfig) (engine.Engine, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%w: bing engine: nil config", engine.ErrInvalidConfig)
	}
	return &bingEngine{
		bingBase: bingBase{
			cfg:     cfg,
			baseURL: overrideStringDef(cfg.Overrides, "base_url", "https://www.bing.com"),
			defCats: []string{"general", "web"},
		},
	}, nil
}

// bingRegion resolves the Bing market/region code for a request. Port of
// bing.py L96-97 (engine_region = traits.get_region(searxng_locale,
// traits.all_locale)): the traits table is not ported, so the search
// processor supplies the region via params.Language. "all" and "clear" are
// the no-region sentinels (bing.py's all_locale for Bing is "clear").
func bingRegion(params *engine.RequestParams) string {
	switch params.Language {
	case "", "all", "clear":
		return ""
	}
	return params.Language
}

// bingOverrideAcceptLanguage ports bing.py's override_accept_language
// (L74-93): when a region is set, the Accept-Language header becomes
// "<region>,<language>;q=0.9" where <language> is the part before the first
// dash (e.g. "en-US,en;q=0.9"). Set() replaces any prior value, mirroring the
// Python dict assignment.
func bingOverrideAcceptLanguage(params *engine.RequestParams, region string) {
	if region == "" {
		return
	}
	if params.Headers == nil {
		params.Headers = make(http.Header)
	}
	lang := region
	if i := strings.IndexByte(region, '-'); i > 0 {
		lang = region[:i]
	}
	params.Headers.Set("Accept-Language", region+","+lang+";q=0.9")
}

// bingLocaleParams ports bing.py's get_locale_params (L54-71): it returns the
// value of the "mkt" query parameter, or "" when the region is empty/"clear"
// (the mkt parameter is then omitted from the query).
func bingLocaleParams(region string) string {
	if region == "" {
		return ""
	}
	return region
}

// bingDecodeCKA ports the Bing redirect decoding in bing.py response()
// (L134-143): hrefs of the form https://www.bing.com/ck/a?u=a1<base64> hide
// the real target URL, base64url-encoded (unpadded) after the "a1" prefix.
// Python pads to a multiple of 4 with '=' before decoding with
// urlsafe_b64decode; on malformed input it raises, here the original href is
// kept instead (defensive, EC-011).
func bingDecodeCKA(href string) string {
	if !strings.HasPrefix(href, "https://www.bing.com/ck/a?") {
		return href
	}
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	uValues := u.Query()["u"]
	if len(uValues) == 0 {
		return href
	}
	uVal := uValues[0]
	if !strings.HasPrefix(uVal, "a1") {
		return href
	}
	// bing.py L139: encoded += '=' * (-len(encoded) % 4). Python's modulo is
	// always >= 0; Go's keeps the sign, so compute the padding explicitly.
	encoded := uVal[2:]
	encoded += strings.Repeat("=", (4-len(encoded)%4)%4)
	decoded, err := base64.URLEncoding.DecodeString(encoded)
	if err != nil {
		return href
	}
	// bing.py L141: decode('utf-8', errors='replace').
	return strings.ToValidUTF8(string(decoded), "\uFFFD")
}

// bingStripAlgoSlug ports bing.py response() L143-147: every <span
// class="algoSlug_icon"> is removed from each <p> of the result item so the
// extracted snippet does not contain the icon glyph. QueryAll materializes
// its result slice before returning, so mutating the tree here is safe.
func bingStripAlgoSlug(item *html.Node) {
	for _, p := range bingQueryAll(item, `.//p`) {
		for _, icon := range bingQueryAll(p, `.//span[@class="algoSlug_icon"]`) {
			if icon.Parent != nil {
				icon.Parent.RemoveChild(icon)
			}
		}
	}
}

// bingQuery is a defensive wrapper around htmlquery.Query: it returns nil on
// evaluation errors. The per-item XPaths are static constants, so an error
// can only signal a programming mistake; returning nil mirrors the Python
// eval_xpath_getindex default of None (defensive, EC-011).
func bingQuery(node *html.Node, expr string) *html.Node {
	n, err := htmlquery.Query(node, expr)
	if err != nil {
		return nil
	}
	return n
}

// bingQueryAll is the defensive wrapper around htmlquery.QueryAll returning
// an empty result on evaluation errors.
func bingQueryAll(node *html.Node, expr string) []*html.Node {
	ns, err := htmlquery.QueryAll(node, expr)
	if err != nil {
		return nil
	}
	return ns
}

// Request mutates params to build the Bing web search request. Port of
// bing.py request() (L96-112). Note that the web module does NOT support
// paging or time ranges (bing.py L9-13: those require JavaScript), so
// params.Pageno and params.TimeRange are ignored — faithful to the Python.
func (e *bingEngine) Request(query string, params *engine.RequestParams) error {
	if params == nil {
		return errors.New("bing engine: nil request params")
	}

	// bing.py L96-99: region resolution + Accept-Language override.
	region := bingRegion(params)
	bingOverrideAcceptLanguage(params, region)

	// bing.py L103-107: query_params = {"q": query, "adlt":
	// _safesearch_map.get(params.get("safesearch", 0), "off")}.
	v := url.Values{}
	v.Set("q", query)
	adlt := "off"
	if a, ok := bingSafeSearchMap[params.SafeSearch]; ok {
		adlt = a
	}
	v.Set("adlt", adlt)

	// bing.py L108-110: merge get_locale_params(engine_region) ("mkt").
	if mkt := bingLocaleParams(region); mkt != "" {
		v.Set("mkt", mkt)
	}

	// bing.py L112: params["url"] = base_url + "/search?" + urlencode(...).
	params.URL = e.baseURL + "/search?" + v.Encode()
	params.Method = http.MethodGet
	return nil
}

// Response parses the Bing web HTML and extracts the organic results. Port of
// bing.py response() (L115-155).
func (e *bingEngine) Response(resp *http.Response) ([]*result.RawResult, error) {
	if resp == nil {
		return nil, errors.New("bing engine: nil http response")
	}

	// bing.py L117: dom = html.fromstring(resp.text).
	doc, err := htmlquery.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("bing engine %q: parse response body: %w", e.cfg.Name, err)
	}

	results := []*result.RawResult{}

	// bing.py L118: for item in eval_xpath_list(dom,
	// '//ol[@id="b_results"]/li[contains(@class, "b_algo")]').
	items, err := htmlquery.QueryAll(doc, `//ol[@id="b_results"]/li[contains(@class, "b_algo")]`)
	if err != nil {
		return nil, fmt.Errorf("bing engine %q: evaluate result xpath: %w", e.cfg.Name, err)
	}
	for _, item := range items {
		// bing.py L119-121: link = eval_xpath_getindex(item, './/h2/a', 0,
		// None); if link is None: continue.
		link := bingQuery(item, `.//h2/a`)
		if link == nil {
			continue
		}
		// bing.py L122-124: href = link.attrib.get("href", "");
		// title = extract_text(link); skip when either is empty.
		href := htmlquery.SelectAttr(link, "href")
		title := extractText([]*html.Node{link})
		if href == "" || title == "" {
			continue
		}

		// bing.py L134-143: decode Bing's /ck/a?u=a1... redirect hrefs.
		href = bingDecodeCKA(href)

		// bing.py L143-147: remove the algoSlug_icon spans before extracting
		// the snippet text.
		bingStripAlgoSlug(item)

		// bing.py L148-155: content = extract_text(content_els).
		results = append(results, result.NewMain(&result.MainResult{
			URL:     href,
			Title:   title,
			Content: extractTextFromNode(item, `.//p`),
		}))
	}

	slog.Debug("bing engine: extracted results", "engine", e.cfg.Name, "count", len(results))
	return results, nil
}
