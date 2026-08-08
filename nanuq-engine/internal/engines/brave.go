// This file is a faithful Go port of SearXNG's searx/engines/brave.py (web
// results, brave_category "search"). Documented deviations vs. the Python
// module are annotated inline and summarised in the RESULT report of this
// task.
package engines

import (
	"context"
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

// braveSafeSearchMap ports brave.py's safesearch_map (L183): the safesearch
// cookie value for safe search off / moderate / strict. Brave carries safe
// search in a cookie, not in a query parameter (the task text mentioned a
// "safe" query parameter; the Python module is the source of truth —
// documented deviation).
var braveSafeSearchMap = map[int]string{
	2: "strict",
	1: "moderate",
	0: "off",
}

// braveTimeRangeMap ports brave.py's time_range_map (L189-194): the tf
// parameter codes. Brave only supports time ranges in the search and goggles
// categories.
var braveTimeRangeMap = map[string]string{
	"day":   "pd",
	"week":  "pw",
	"month": "pm",
	"year":  "py",
}

// braveEngine implements engine.Engine for Brave web results (brave.py,
// brave_category "search"). The news / images / videos categories of the
// Python module rely on JS/JSON payload parsing (extract_json_data,
// L243-261) that is not ported; a brave engine configured for those
// categories fails in Response with a descriptive error (documented
// deviation).
type braveEngine struct {
	cfg *config.EngineConfig

	// baseURL ports the base_url module attribute (brave.py L152). SearXNG
	// settings may override module attributes, so it is read from
	// cfg.Overrides["base_url"] with the Python default as fallback.
	baseURL string

	// defCats are the module's categories (brave.py L153: categories = [] —
	// the YAML entry declares them).
	defCats []string

	// braveCategory ports the brave_category module attribute (brave.py
	// L154): only "search" and "goggles" are supported by this port.
	braveCategory string

	// goggles ports the Goggles module attribute (brave.py L164).
	goggles string

	// spellcheck ports the brave_spellcheck module attribute (brave.py
	// L167).
	spellcheck bool
}

// NewBraveEngine builds one Brave engine per YAML entry.
func NewBraveEngine(cfg *config.EngineConfig) (engine.Engine, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%w: brave engine: nil config", engine.ErrInvalidConfig)
	}
	return &braveEngine{
		cfg:           cfg,
		baseURL:       overrideStringDef(cfg.Overrides, "base_url", "https://search.brave.com/"),
		defCats:       []string{},
		braveCategory: overrideStringDef(cfg.Overrides, "brave_category", "search"),
		goggles:       overrideStringDef(cfg.Overrides, "goggles", ""),
		spellcheck:    overrideBoolDef(cfg.Overrides, "brave_spellcheck", false),
	}, nil
}

// Name returns the engine's configured name (YAML entry name).
func (e *braveEngine) Name() string { return e.cfg.Name }

// Shortcut returns the configured shortcut.
func (e *braveEngine) Shortcut() string { return e.cfg.Shortcut }

// Categories returns the configured categories, falling back to the module's
// default categories when the YAML entry declares none.
func (e *braveEngine) Categories() []string {
	if len(e.cfg.Categories) > 0 {
		return e.cfg.Categories
	}
	return e.defCats
}

// NeedsInit reports that no per-engine init is required.
func (e *braveEngine) NeedsInit() bool { return false }

// Setup is a no-op for the Brave engine (all config comes from Overrides).
func (e *braveEngine) Setup(_ context.Context, _ *config.EngineConfig) error { return nil }

// Init is a no-op. brave.py's init fetches the engine traits (L418-494
// fetch_traits); that table is out of scope for this port — the search
// processor supplies the locale directly via params.Language (see
// braveRegion and braveUILang).
func (e *braveEngine) Init(_ context.Context) error { return nil }

// braveRegion resolves the region used for the "country" cookie. Port of
// brave.py L226-227: engine_region = traits.get_region(searxng_locale, "all")
// and country = engine_region.split("-")[-1].lower(). The traits table is
// not ported, so the search processor supplies the locale via
// params.Language; "all" is the no-region sentinel.
func braveRegion(params *engine.RequestParams) string {
	lang := params.Language
	switch lang {
	case "", "all", "clear":
		return "all"
	}
	if i := strings.LastIndexByte(lang, '-'); i > 0 {
		lang = lang[i+1:]
	}
	return strings.ToLower(lang)
}

// braveUILang resolves the "ui_lang" cookie. Port of brave.py L229:
// locales.get_engine_locale(searxng_locale, traits.custom["ui_lang"],
// "en-us"). The traits map is not ported; the lower-cased locale is used as
// an approximation ("en-US" → "en-us", matching the pattern of the trait
// values) (documented deviation).
func braveUILang(params *engine.RequestParams) string {
	lang := params.Language
	switch lang {
	case "", "all", "clear":
		return "en-us"
	}
	return strings.ToLower(lang)
}

// Request mutates params to build the Brave search request. Port of brave.py
// request() (L197-231).
func (e *braveEngine) Request(query string, params *engine.RequestParams) error {
	if params == nil {
		return errors.New("brave engine: nil request params")
	}

	// brave.py L199-202: args = {"q": query, "source": "web"}.
	v := url.Values{}
	v.Set("q", query)
	v.Set("source", "web")

	// brave.py L203-204: optional spellcheck parameter.
	if e.spellcheck {
		v.Set("spellcheck", "1")
	}

	// brave.py L206-210: paging and time ranges are only supported in the
	// search and goggles categories.
	if e.braveCategory == "search" || e.braveCategory == "goggles" {
		if params.Pageno > 1 {
			v.Set("offset", strconv.Itoa(params.Pageno-1))
		}
		if tf, ok := braveTimeRangeMap[params.TimeRange]; ok {
			v.Set("tf", tf)
		}
	}

	// brave.py L212-213: goggles_id for the goggles category.
	if e.braveCategory == "goggles" {
		v.Set("goggles_id", e.goggles)
	}

	// brave.py L215-216: Accept-Encoding header and the request URL.
	if params.Headers == nil {
		params.Headers = make(http.Header)
	}
	params.Headers.Set("Accept-Encoding", "gzip, deflate")
	params.URL = e.baseURL + e.braveCategory + "?" + v.Encode()
	params.Method = http.MethodGet

	// brave.py L221-230: cookies. The Python assigns each cookie key on the
	// existing dict, so values are replaced, not duplicated (see
	// setEngineCookie).
	safe := "off"
	if s, ok := braveSafeSearchMap[params.SafeSearch]; ok {
		safe = s
	}
	setEngineCookie(params, "safesearch", safe)
	setEngineCookie(params, "useLocation", "0")
	setEngineCookie(params, "summarizer", "0")
	setEngineCookie(params, "country", braveRegion(params))
	setEngineCookie(params, "ui_lang", braveUILang(params))
	return nil
}

// Response parses the Brave HTML and extracts the organic results plus any
// related-query suggestions. Port of brave.py response() (L264-286, only the
// search/goggles path) and _parse_search (L289-349).
func (e *braveEngine) Response(resp *http.Response) ([]*result.RawResult, error) {
	if resp == nil {
		return nil, errors.New("brave engine: nil http response")
	}

	// brave.py L266-286: news / images / videos require JS payload parsing
	// (_parse_news/_parse_images/_parse_videos via extract_json_data) that
	// is not ported; the Python raises for any category it cannot handle.
	if e.braveCategory != "search" && e.braveCategory != "goggles" {
		return nil, fmt.Errorf("brave engine %q: unsupported brave category %q (news, images and videos are not ported)", e.cfg.Name, e.braveCategory)
	}

	// brave.py L291: dom = html.fromstring(resp.text).
	doc, err := htmlquery.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("brave engine %q: parse response body: %w", e.cfg.Name, err)
	}

	results := []*result.RawResult{}

	// brave.py L293: results = '//div[contains(@class, 'snippet ')]' (note
	// the trailing space in the class filter).
	items, err := htmlquery.QueryAll(doc, `//div[contains(@class, 'snippet ')]`)
	if err != nil {
		return nil, fmt.Errorf("brave engine %q: evaluate result xpath: %w", e.cfg.Name, err)
	}
	for _, item := range items {
		// brave.py L294-296: url and title_tag; items without a URL, a
		// title or a URL host are ads and skipped.
		urlNode := bingQuery(item, `.//a/@href`)
		if urlNode == nil {
			continue
		}
		rawURL := extractText([]*html.Node{urlNode})
		titleTag := bingQuery(item, `.//div[contains(@class, 'title')]`)
		if titleTag == nil {
			continue
		}
		// brave.py L296: not urlparse(url).netloc → ad (a relative URL has
		// no host).
		if u, uerr := url.Parse(rawURL); uerr != nil || u.Host == "" {
			continue
		}

		// brave.py L299-318: content with the published-date prefix
		// stripped. The dateutil parsing (_extract_published_date, L234-240)
		// is not ported — MainResult has no published-date field — but the
		// lstrip of the raw date text is preserved.
		content := ""
		contentTag := bingQuery(item, `.//div[contains(concat(' ', @class, ' '), ' content ')]`)
		if contentTag != nil {
			content = extractText([]*html.Node{contentTag})
			if pubDate := extractTextFromNode(contentTag, `.//span[contains(@class, 't-secondary')]`); pubDate != "" {
				// brave.py L318: content.lstrip(_pub_date).strip("- \n\t").
				// Python lstrip treats its argument as a character set,
				// exactly like Go's strings.TrimLeft.
				content = strings.TrimLeft(content, pubDate)
				content = strings.Trim(content, "- \n\t")
			}
		}

		// brave.py L320: thumbnail = first './/a[contains(@class,
		// 'thumbnail')]//img/@src' (attribute node, see extractText).
		thumbnail := ""
		if srcNode := bingQuery(item, `.//a[contains(@class, 'thumbnail')]//img/@src`); srcNode != nil {
			thumbnail = extractText([]*html.Node{srcNode})
		}

		// brave.py L322-330: the result is added before the video check.
		results = append(results, result.NewMain(&result.MainResult{
			URL:       rawURL,
			Title:     extractText([]*html.Node{titleTag}),
			Content:   content,
			Thumbnail: thumbnail,
		}))

		// brave.py L332-344: a video-snippet would get an iframe via
		// get_embeded_stream_url; MainResult has no iframe field, so the
		// video handling is not ported (documented deviation).
	}

	// brave.py L346-347: related-query suggestions. The Python appends
	// every match; the port skips empty texts (defensive, EC-011, matching
	// the other engine ports of this package).
	suggestions, err := htmlquery.QueryAll(doc, `//a[contains(@class, 'related-query')]`)
	if err == nil {
		for _, s := range suggestions {
			if text := extractText([]*html.Node{s}); text != "" {
				results = append(results, result.NewSuggestion(text))
			}
		}
	}

	slog.Debug("brave engine: extracted results", "engine", e.cfg.Name, "count", len(results))
	return results, nil
}
