// duckduckgo.go implements the DuckDuckGo engine family for nanuq-engine.
//
// It is a faithful Go port of four SearXNG modules that share the DuckDuckGo
// frontend:
//
//   - duckduckgo.py           — no-JS HTML web search (POST form to DDG-html)
//   - duckduckgo_web.py       — d.js JSON web search (deep_preload_link scrape)
//   - duckduckgo_extra.py     — images / videos / news (i.js / v.js / news.js)
//   - duckduckgo_weather.py   — weather (js/spice/forecast JSONP)
//
// Following the 1:N module pattern (DSG-003, REQ-004) a single module serves
// every instance: NewDuckDuckGoEngine discriminates the behaviour from
// EngineConfig (the ddg_category override first, then the configured
// categories) and never from the YAML instance name. There is deliberately no
// Register function — wiring happens through the exported constructor.
//
// Documented deviations from the Python modules (all required by the Go
// engine contract, REQ-005):
//
//   - duckduckgo_web.py needs I/O inside request() (_fetch_first_page_link,
//     L55-92, scrapes deep_preload_link then fetches d.js JSON), which the Go
//     contract forbids (Request is a pure mutation). The duckduckgo_web
//     instance therefore routes to the shared no-JS HTML flow
//     (duckduckgo.py) — the spec's Request description (general ->
//     html.duckduckgo.com/html/?q=) is followed literally.
//   - The vqd ("validation query digest") cache (duckduckgo.py L216-251 and
//     duckduckgo_extra.py fetch_vqd) is not ported: it is a persistent
//     SQLite cache and its acquisition needs extra network round-trips. The
//     vqd form-data / query-string keys are still emitted (empty value) so
//     the request shape matches DDG's expectations; pagination beyond page 1
//     is impossible without a vqd (see the EngineSuspendError in
//     requestHTML).
//   - gen_useragent() (duckduckgo.py L220) performs an HTTP round-trip; a
//     fixed browser-like UA replaces it. The UA must be static anyway so the
//     vqd value stays reusable (duckduckgo.py L380-382).
//   - quote_ddg_bangs (duckduckgo.py L346-359) only quotes bangs listed in
//     the EXTERNAL_BANGS data file; without that file every bang token is
//     quoted, which is a safe superset (it only affects !bang queries).
//   - Content-Type is NOT set in params.Headers for the HTML form request:
//     the Go network layer (internal/network buildRequest) already sets it
//     automatically when Data is non-nil, and setting it again would produce
//     a duplicate header (duckduckgo.py L451).
//   - weather location is derived from the request URL instead of
//     GeoLocation.by_query() geocoding (duckduckgo_weather.py L112), which
//     is a network-layer concern in SearXNG.
//   - The result.WeatherAnswer model is the minimal port of TASK-004
//     (Temperature/Condition/Location/Units only), so feels_like, wind,
//     pressure, humidity, cloud_cover and the hourly forecast list are
//     dropped (duckduckgo_weather.py L74-123).
//   - Extra-mode results map onto MainResult (+Template): the Python image
//     resolution/source and video length/metadata fields have no Go
//     equivalent (duckduckgo_extra.py L150-174).
//   - The 500-char query and zh-locale guards clear params.URL (Python
//     params["url"] = None, duckduckgo.py L366/L426): the network layer
//     turns an empty URL into an error + brief suspension instead of a
//     silent skip — closest behaviour available without I/O in Request.
package engines

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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

// ddgMode discriminates the three DuckDuckGo module behaviours served by one
// Go implementation (1:N pattern): html web, extra (images/videos/news) and
// weather.
type ddgMode int

const (
	// ddgModeHTML is the no-JS HTML web flow (duckduckgo.py); it also serves
	// duckduckgo_web instances (see the file header deviation note).
	ddgModeHTML ddgMode = iota
	// ddgModeExtra is the images/videos/news JSON flow (duckduckgo_extra.py).
	ddgModeExtra
	// ddgModeWeather is the spice forecast flow (duckduckgo_weather.py).
	ddgModeWeather
)

const (
	// ddgURL is duckduckgo.py L210: the DDG-html no-JS search endpoint.
	ddgURL = "https://html.duckduckgo.com/html/"

	// ddgBaseURL is the duckduckgo.com origin shared by the extra
	// (i.js / v.js / news.js) and weather (spice) endpoints.
	ddgBaseURL = "https://duckduckgo.com"

	// ddgWeatherBaseURL is duckduckgo_weather.py L33: base_url.
	ddgWeatherBaseURL = "https://duckduckgo.com/js/spice/forecast/"

	// ddgUserAgent replaces gen_useragent() (duckduckgo.py L220, L382): the
	// Python helper performs an HTTP round-trip which is forbidden in the Go
	// engine layer. A fixed browser-like UA is used — the value only needs to
	// stay static across requests (the vqd value is generated from it).
	ddgUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:115.0) Gecko/20100101 Firefox/115.0"
)

// ddgTimeRangeDict is duckduckgo.py L214: time_range_support values map to
// the DDG form field "df".
var ddgTimeRangeDict = map[string]string{"day": "d", "week": "w", "month": "m", "year": "y"}

// ddgSearchPathMap is duckduckgo_extra.py L44: the *.js API endpoint per
// extra category.
var ddgSearchPathMap = map[string]string{"images": "i", "videos": "v", "news": "news"}

// ddgSafeSearchCookies and ddgSafeSearchArgs are duckduckgo_extra.py L41-42.
// Deviation: Python maps level 1 to None (no cookie/argument sent) — the
// missing key here has exactly that effect, so only levels 0 and 2 are
// present.
var (
	ddgSafeSearchCookies = map[int]string{0: "-2", 2: "1"}
	ddgSafeSearchArgs    = map[int]string{0: "1", 2: "1"}
)

// weatherKitToCondition is duckduckgo_weather.py L36-71: Apple WeatherKit
// condition codes mapped onto the SearXNG weather condition vocabulary.
var weatherKitToCondition = map[string]string{
	"BlowingDust":            "fog",
	"Clear":                  "clear sky",
	"Cloudy":                 "cloudy",
	"Foggy":                  "fog",
	"Haze":                   "fog",
	"MostlyClear":            "clear sky",
	"MostlyCloudy":           "partly cloudy",
	"PartlyCloudy":           "partly cloudy",
	"Smoky":                  "fog",
	"Breezy":                 "partly cloudy",
	"Windy":                  "partly cloudy",
	"Drizzle":                "light rain",
	"HeavyRain":              "heavy rain",
	"IsolatedThunderstorms":  "rain and thunder",
	"Rain":                   "rain",
	"SunShowers":             "rain",
	"ScatteredThunderstorms": "heavy rain and thunder",
	"StrongStorms":           "heavy rain and thunder",
	"Thunderstorms":          "rain and thunder",
	"Frigid":                 "clear sky",
	"Hail":                   "heavy rain",
	"Hot":                    "clear sky",
	"Flurries":               "light snow",
	"Sleet":                  "sleet",
	"Snow":                   "light snow",
	"SunFlurries":            "light snow",
	"WintryMix":              "sleet",
	"Blizzard":               "heavy snow",
	"BlowingSnow":            "heavy snow",
	"FreezingDrizzle":        "light sleet",
	"FreezingRain":           "sleet",
	"HeavySnow":              "heavy snow",
	"Hurricane":              "rain and thunder",
	"TropicalStorm":          "rain and thunder",
}

// duckDuckGoEngine implements engine.Engine for the whole DuckDuckGo family.
// One instance is created per YAML engine entry (1:N); mode and category are
// fixed at construction time.
type duckDuckGoEngine struct {
	cfg           *config.EngineConfig
	mode          ddgMode
	extraCategory string // images | videos | news (ddgModeExtra only)
	logger        *slog.Logger
}

// NewDuckDuckGoEngine builds one DuckDuckGo engine instance from a single
// YAML entry. The behaviour is discriminated as follows (1:N pattern, see the
// file header):
//
//  1. the ddg_category override (settings.yml pattern of duckduckgo_extra
//     instances) selects the extra mode and must be one of images, videos or
//     news — anything else wraps engine.ErrInvalidConfig, the Go port of the
//     ValueError raised by duckduckgo_extra.py init() (L50-53);
//  2. otherwise the configured categories decide: weather -> weather mode,
//     images/videos/news -> extra mode, anything else -> html web mode
//     (duckduckgo.py; also duckduckgo_web instances, see file header).
func NewDuckDuckGoEngine(cfg *config.EngineConfig) (engine.Engine, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%w: duckduckgo engine: nil config", engine.ErrInvalidConfig)
	}

	e := &duckDuckGoEngine{cfg: cfg, logger: slog.Default()}

	// duckduckgo_extra.py init() (L50-53): the ddg_category override is
	// validated at construction time.
	if cat, ok := overrideString(cfg.Overrides, "ddg_category"); ok {
		if !validExtraCategory(cat) {
			return nil, fmt.Errorf("%w: duckduckgo engine %q: unsupported DuckDuckGo category %q (want images, videos or news)",
				engine.ErrInvalidConfig, cfg.Name, cat)
		}
		e.mode = ddgModeExtra
		e.extraCategory = cat
		return e, nil
	}

	// Otherwise derive the mode from the configured categories (the Python
	// modules declare their categories at module level).
	for _, cat := range cfg.Categories {
		switch cat {
		case "weather":
			e.mode = ddgModeWeather
			return e, nil
		case "images", "videos", "news":
			e.mode = ddgModeExtra
			e.extraCategory = cat
			return e, nil
		}
	}

	// Default: no-JS HTML web mode (duckduckgo.py categories general/web;
	// also duckduckgo_web.py general instances, see file header).
	e.mode = ddgModeHTML
	return e, nil
}

// validExtraCategory reports whether cat is a supported duckduckgo_extra
// category (duckduckgo_extra.py L52-53).
func validExtraCategory(cat string) bool {
	switch cat {
	case "images", "videos", "news":
		return true
	}
	return false
}

// Name returns the engine's configured name (YAML entry name).
func (e *duckDuckGoEngine) Name() string { return e.cfg.Name }

// Shortcut returns the configured shortcut.
func (e *duckDuckGoEngine) Shortcut() string { return e.cfg.Shortcut }

// Categories returns the configured categories.
func (e *duckDuckGoEngine) Categories() []string { return []string(e.cfg.Categories) }

// NeedsInit reports that no per-engine init is required.
func (e *duckDuckGoEngine) NeedsInit() bool { return false }

// Setup is a no-op for the DuckDuckGo family (all config is static).
func (e *duckDuckGoEngine) Setup(_ context.Context, _ *config.EngineConfig) error { return nil }

// Init is a no-op for the DuckDuckGo family.
func (e *duckDuckGoEngine) Init(_ context.Context) error { return nil }

// Request dispatches to the request builder of the instance's mode. It
// mutates params without I/O (REQ-005).
func (e *duckDuckGoEngine) Request(query string, params *engine.RequestParams) error {
	if params == nil {
		return errors.New("duckduckgo engine: nil request params")
	}
	switch e.mode {
	case ddgModeExtra:
		return e.requestExtra(query, params)
	case ddgModeWeather:
		return e.requestWeather(query, params)
	default:
		return e.requestHTML(query, params)
	}
}

// requestHTML builds the no-JS HTML form request — port of duckduckgo.py
// request() (L362-456).
func (e *duckDuckGoEngine) requestHTML(query string, params *engine.RequestParams) error {
	// duckduckgo.py L364-367: DDG rejects queries with more than 499 chars;
	// the Python clears params["url"] which silently skips the request. The
	// Go network layer turns the empty URL into an error (see file header).
	if len(query) >= 500 {
		params.URL = ""
		return nil
	}

	// duckduckgo.py L369: quote_ddg_bangs() (L346-359).
	query = quoteDuckDuckGoBangs(query)

	// HTTP headers (duckduckgo.py L378-393).
	headers := params.Headers
	if headers == nil {
		headers = make(http.Header)
		params.Headers = headers
	}
	headers.Set("User-Agent", ddgUserAgent)      // L382
	headers.Set("Sec-Fetch-Dest", "document")    // L384
	headers.Set("Sec-Fetch-Mode", "navigate")    // L385
	headers.Set("Sec-Fetch-Site", "same-origin") // L386
	headers.Set("Sec-Fetch-User", "?1")          // L387
	headers.Set("Referer", ddgURL)               // L389, overwritten to ddg_url at L452
	if headers.Get("Accept-Language") == "" && params.Language != "" && params.Language != "all" {
		// duckduckgo.py L391-393: accept-language from the UI locale.
		headers.Set("Accept-Language", fmt.Sprintf("%s,%s-%s;q=0.7",
			params.Language, params.Language, strings.ToUpper(params.Language)))
	}
	// duckduckgo.py L451 Content-Type is left to the Go network layer: it is
	// set automatically for Data bodies and would duplicate otherwise.

	// DDG search form (POST data) — duckduckgo.py L395-449.
	data := params.Data
	if data == nil {
		data = make(map[string]string)
		params.Data = data
	}
	data["q"] = query // L402
	params.Method = http.MethodPost
	params.URL = ddgURL // L403

	if params.Pageno <= 1 {
		data["b"] = "" // L406-407: first page
	} else {
		// duckduckgo.py L408-421: follow-up pages require the vqd value; it
		// is generated from the query + UA and cached (L230-251). The Go
		// port has no vqd cache, so the value is always missing and DDG
		// would flag the request as a bot — mirror the
		// SearxEngineCaptchaException(suspended_time=0) of L418-421.
		vqd := "" // get_vqd()/set_vqd() not ported, see file header
		if vqd != "" {
			// Unreachable in this port; kept to mirror the Python control
			// flow. duckduckgo.py L423-427: some zh locales have no "next
			// page" button; DDG returns a 403 for such pages.
			data["vqd"] = vqd
			if strings.HasPrefix(params.Language, "zh") {
				params.URL = ""
				return nil
			}
			// duckduckgo.py L429-436: next-page form fields.
			data["nextParams"] = ""
			data["api"] = "d.js"
			data["o"] = "json"
			data["v"] = "l"
			offset := 10 + (params.Pageno-2)*15 // Page 2 = 10, Page 2+n = 10 + n*15
			data["dc"] = strconv.Itoa(offset + 1)
			data["s"] = strconv.Itoa(offset)
		} else {
			// duckduckgo.py L414-421.
			return &engine.EngineSuspendError{
				Reason:     fmt.Sprintf("VQD missed (page: %d)", params.Pageno),
				SuspendFor: 0, // L418: suspended_time=0 -> no backoff
			}
		}
	}

	// duckduckgo.py L438-444: region ("kl"). Without trait support the
	// "All regions" value is always sent; no kl cookie is set because DDG
	// only stores kl in a cookie when a concrete region is requested.
	data["kl"] = "wt-wt"

	// duckduckgo.py L446-449: time range filter ("df").
	if v, ok := ddgTimeRangeDict[params.TimeRange]; ok {
		data["df"] = v
		params.Cookies = append(params.Cookies, &http.Cookie{Name: "df", Value: v})
	}

	e.logger.Debug("duckduckgo: html request built", "engine", e.cfg.Name, "query", query)
	return nil
}

// requestExtra builds the images/videos/news JSON request — port of
// duckduckgo_extra.py request() (L86-147).
func (e *duckDuckGoEngine) requestExtra(query string, params *engine.RequestParams) error {
	// duckduckgo_extra.py L88-91: same 499-char limit as the web module.
	if len(query) >= 500 {
		params.URL = ""
		return nil
	}

	// HTTP headers (duckduckgo_extra.py L96-104).
	headers := params.Headers
	if headers == nil {
		headers = make(http.Header)
		params.Headers = headers
	}
	headers.Set("User-Agent", ddgUserAgent)           // L99
	headers.Set("Accept", "*/*")                      // L102
	headers.Set("Referer", "https://duckduckgo.com/") // L103
	// L104 Host is kept for fidelity, but net/http derives the Host header
	// from the URL and ignores this value (Go http.Request.Host is the only
	// override).
	headers.Set("Host", "duckduckgo.com")

	// duckduckgo_extra.py L100: vqd = get_vqd() or fetch_vqd(); both need a
	// cache/network so the argument is sent empty (URL shape preserved).
	vqd := ""

	// DDG XHR arguments (duckduckgo_extra.py L117-125).
	args := url.Values{}
	args.Set("o", "json")
	args.Set("q", query)
	args.Set("u", "bing")
	args.Set("l", "wt-wt") // eng_region without traits support
	args.Set("bpia", "1")
	args.Set("vqd", vqd)
	args.Set("a", "h_")

	// Cookies (duckduckgo_extra.py L127-129): ad = engine language, ah and l
	// = engine region. Without trait support both default to wt-wt; ad uses
	// the request language when one is set.
	ad := "wt-wt"
	if params.Language != "" && params.Language != "all" {
		ad = params.Language
	}
	params.Cookies = append(params.Cookies,
		&http.Cookie{Name: "ad", Value: ad},
		&http.Cookie{Name: "ah", Value: "wt-wt"},
		&http.Cookie{Name: "l", Value: "wt-wt"},
	)

	// duckduckgo_extra.py L131-133: ct = "EN" unless a concrete locale is
	// requested (the territory part is dropped: "es-AR" -> "ES").
	args.Set("ct", "EN")
	if params.Language != "" && params.Language != "all" {
		args.Set("ct", strings.ToUpper(strings.SplitN(params.Language, "-", 2)[0]))
	}

	// duckduckgo_extra.py L135-136: page offset.
	if params.Pageno > 1 {
		args.Set("s", strconv.Itoa((params.Pageno-1)*100))
	}

	// duckduckgo_extra.py L138-141 + L41-42: safe-search cookie and query
	// argument are only sent when the level maps to a concrete value.
	if v, ok := ddgSafeSearchCookies[params.SafeSearch]; ok {
		params.Cookies = append(params.Cookies, &http.Cookie{Name: "p", Value: v})
		args.Set("p", ddgSafeSearchArgs[params.SafeSearch])
	}

	// duckduckgo_extra.py L143.
	params.Method = http.MethodGet
	params.URL = ddgBaseURL + "/" + ddgSearchPathMap[e.extraCategory] + ".js?" + args.Encode()
	return nil
}

// requestWeather builds the spice forecast request — port of
// duckduckgo_weather.py request() (L89-101).
func (e *duckDuckGoEngine) requestWeather(query string, params *engine.RequestParams) error {
	// duckduckgo_weather.py L94-98: cookies — ad = engine language (default
	// en_US in get_ddg_lang), ah/l = engine region (default wt-wt).
	ad := "en_US"
	if params.Language != "" && params.Language != "all" {
		ad = params.Language
	}
	params.Cookies = append(params.Cookies,
		&http.Cookie{Name: "ad", Value: ad},
		&http.Cookie{Name: "ah", Value: "wt-wt"},
		&http.Cookie{Name: "l", Value: "wt-wt"},
	)

	// duckduckgo_weather.py L100: base_url.format(query=quote(query),
	// lang=eng_lang.split('_')[0]). Python quote() encodes spaces as %20 —
	// url.PathEscape does the same.
	lang := "en" // eng_lang default "en_US" -> "en"
	if params.Language != "" && params.Language != "all" {
		lang = strings.SplitN(params.Language, "_", 2)[0]
		lang = strings.SplitN(lang, "-", 2)[0]
	}
	params.Method = http.MethodGet
	params.URL = ddgWeatherBaseURL + url.PathEscape(query) + "/" + lang
	return nil
}

// Response dispatches to the response parser of the instance's mode.
func (e *duckDuckGoEngine) Response(resp *http.Response) ([]*result.RawResult, error) {
	if resp == nil {
		return nil, errors.New("duckduckgo engine: nil http response")
	}
	switch e.mode {
	case ddgModeExtra:
		return e.responseExtra(resp)
	case ddgModeWeather:
		return e.responseWeather(resp)
	default:
		return e.responseHTML(resp)
	}
}

// responseHTML parses the DDG-html results page — port of duckduckgo.py
// response() (L466-519).
func (e *duckDuckGoEngine) responseHTML(resp *http.Response) ([]*result.RawResult, error) {
	// duckduckgo.py L469-470: a 303 redirect carries no results. (303 is
	// < 400, so the Go network layer lets it reach Response.)
	if resp.StatusCode == http.StatusSeeOther {
		return nil, nil
	}

	doc, err := htmlquery.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("duckduckgo engine %q: parse response body: %w", e.cfg.Name, err)
	}

	// duckduckgo.py L459-463 is_ddg_captcha() + L475-477: on CAPTCHA DDG
	// serves its own "not a robot" dialog. Response errors are wrapped by
	// the processor, so this does suspend the engine (suspended_time=0).
	captchaForm, err := htmlquery.QueryAll(doc, "//form[@id='challenge-form']")
	if err != nil {
		return nil, fmt.Errorf("duckduckgo engine %q: evaluate challenge-form xpath: %w", e.cfg.Name, err)
	}
	if len(captchaForm) > 0 {
		return nil, &engine.EngineSuspendError{Reason: "CAPTCHA (kl=wt-wt)", SuspendFor: 0}
	}

	// duckduckgo.py L479-491: the vqd value is read from the response form
	// and cached for follow-up pages. The Go port has no vqd cache (see the
	// file header), so this step is skipped.

	out := []*result.RawResult{}

	// duckduckgo.py L493-503: select only "web-result" divs and ignore the
	// ad blocks (class "result--ad result--ad--small").
	divs, err := htmlquery.QueryAll(doc, `//div[@id="links"]/div[contains(@class, "web-result")]`)
	if err != nil {
		return nil, fmt.Errorf("duckduckgo engine %q: evaluate web-result xpath: %w", e.cfg.Name, err)
	}
	for _, div := range divs {
		title := extractTextFromNode(div, ".//h2/a") // L495
		hrefs := queryNodes(div, ".//h2/a/@href")
		if len(hrefs) == 0 {
			// duckduckgo.py L500: url = eval_xpath(...)[0] — an IndexError
			// drops the result.
			continue
		}
		url := strings.TrimSpace(htmlquery.InnerText(hrefs[0]))
		if url == "" {
			continue
		}
		// duckduckgo.py L496-497: eval_xpath_getindex(..., 0, []) returns []
		// when missing -> empty content.
		content := extractTextFromNode(div, `.//a[contains(@class, "result__snippet")]`)
		out = append(out, result.NewMain(&result.MainResult{Title: title, URL: url, Content: content}))
	}

	// duckduckgo.py L505-518: the zero-click instant answer.
	zeroClick := strings.TrimSpace(extractTextFromNode(doc, `//div[@id="zero_click_abstract"]`))
	if zeroClick != "" &&
		!strings.Contains(zeroClick, "Your IP address is") &&
		!strings.Contains(zeroClick, "Your user agent:") &&
		!strings.Contains(zeroClick, "URL Decoded:") {
		// The Python Answer also carries a url (L516); the Go result.Answer
		// model has no URL field, so it is dropped.
		out = append(out, result.NewAnswer(&result.AnswerSet{
			Answers: []result.Answer{{Content: zeroClick}},
		}))
	}

	e.logger.Debug("duckduckgo: html response parsed", "engine", e.cfg.Name, "count", len(out))
	return out, nil
}

// responseExtra parses the i.js / v.js / news.js JSON response — port of
// duckduckgo_extra.py response() (L187-201) and the _image_result /
// _video_result / _news_result helpers (L150-184).
func (e *duckDuckGoEngine) responseExtra(resp *http.Response) ([]*result.RawResult, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("duckduckgo engine %q: read response body: %w", e.cfg.Name, err)
	}
	data, err := decodeJSON(body)
	if err != nil {
		return nil, fmt.Errorf("duckduckgo engine %q: invalid JSON response: %w", e.cfg.Name, err)
	}
	root, ok := data.(map[string]any)
	if !ok {
		return nil, nil
	}
	resultsVal, ok := root["results"] // duckduckgo_extra.py L191
	if !ok {
		return nil, nil
	}
	items, ok := resultsVal.([]any)
	if !ok {
		return nil, nil
	}

	out := make([]*result.RawResult, 0, len(items))
	for _, item := range items {
		r, ok := item.(map[string]any)
		if !ok {
			continue
		}
		var rr *result.RawResult
		switch e.extraCategory {
		case "images": // _image_result, L150-160
			rr = result.NewMain(&result.MainResult{
				URL:       toString(r["url"]),
				Title:     toString(r["title"]),
				Thumbnail: toString(r["thumbnail"]),
				ImgSrc:    toString(r["image"]),
				Template:  "images.html",
			})
		case "videos": // _video_result, L163-174
			thumb := ""
			if imgMap, ok := r["images"].(map[string]any); ok {
				thumb = toString(imgMap["small"]) // L169: small or medium
				if thumb == "" {
					thumb = toString(imgMap["medium"])
				}
			}
			rr = result.NewMain(&result.MainResult{
				URL:       toString(r["content"]),
				Title:     toString(r["title"]),
				Content:   toString(r["description"]),
				Thumbnail: thumb,
				Template:  "videos.html",
			})
		case "news": // _news_result, L177-184
			rr = result.NewMain(&result.MainResult{
				URL:      toString(r["url"]),
				Title:    toString(r["title"]),
				Content:  htmlToText(toString(r["excerpt"])),
				Template: "news.html",
			})
		}
		out = append(out, rr)
	}
	return out, nil
}

// responseWeather parses the spice forecast JSONP response — port of
// duckduckgo_weather.py response() (L104-126).
func (e *duckDuckGoEngine) responseWeather(resp *http.Response) ([]*result.RawResult, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("duckduckgo engine %q: read response body: %w", e.cfg.Name, err)
	}

	// duckduckgo_weather.py L107-108: the empty JSONP callback means no
	// weather data for the query.
	if strings.TrimSpace(string(body)) == "ddg_spice_forecast();" {
		return nil, nil
	}

	// duckduckgo_weather.py L110: the JSON document sits between the first
	// and the second-to-last newline of the JSONP envelope, which has the
	// shape "ddg_spice_forecast(\n<json>\n);\n" (trailing newline required —
	// verified empirically against the Python slice semantics).
	firstNL := bytes.IndexByte(body, '\n')
	lastNL := bytes.LastIndexByte(body, '\n')
	if firstNL < 0 || lastNL-2 <= firstNL {
		return nil, fmt.Errorf("duckduckgo engine %q: unexpected forecast envelope", e.cfg.Name)
	}
	data, err := decodeJSON(body[firstNL+1 : lastNL-2])
	if err != nil {
		return nil, fmt.Errorf("duckduckgo engine %q: invalid forecast JSON: %w", e.cfg.Name, err)
	}
	root, ok := data.(map[string]any)
	if !ok {
		return nil, nil
	}
	current, ok := root["currentWeather"].(map[string]any)
	if !ok {
		return nil, nil
	}

	// duckduckgo_weather.py L74-86 _weather_data(): temperature in °C and a
	// condition mapped through WEATHERKIT_TO_CONDITION. feels_like, wind,
	// pressure, humidity, cloud_cover and the hourly forecast list (L89-123)
	// are dropped: result.WeatherAnswer only models
	// Temperature/Condition/Location/Units (TASK-004 minimal port).
	w := &result.WeatherAnswer{
		Temperature: toString(current["temperature"]) + "°C",
		// Deviation: the Python dict lookup raises KeyError on an unknown
		// code and drops the whole answer; Go keeps the raw code instead so
		// results keep flowing (duckduckgo_weather.py L79).
		Condition: weatherKitCondition(toString(current["conditionCode"])),
		Location:  e.weatherLocation(resp),
		Units:     "metric",
	}
	return []*result.RawResult{result.NewWeather(w)}, nil
}

// weatherKitCondition maps a WeatherKit condition code to the SearXNG
// vocabulary, falling back to the raw code (see the deviation note in
// responseWeather).
func weatherKitCondition(code string) string {
	if v, ok := weatherKitToCondition[code]; ok {
		return v
	}
	return code
}

// weatherLocation derives the query location from the request URL path
// (.../forecast/{query}/{lang}). The Python uses GeoLocation.by_query()
// geocoding (duckduckgo_weather.py L112), which is out of scope for the Go
// engine layer. The URL path is already decoded by net/url, so the segment
// is used as-is.
func (e *duckDuckGoEngine) weatherLocation(resp *http.Response) string {
	if resp.Request == nil || resp.Request.URL == nil {
		return ""
	}
	parts := strings.Split(strings.Trim(resp.Request.URL.Path, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-2]
}

// quoteDuckDuckGoBangs quotes !bang directives so DDG does not redirect —
// port of duckduckgo.py quote_ddg_bangs() (L346-359). Deviation: the Python
// version only quotes bangs present in the EXTERNAL_BANGS data file; every
// bang token is quoted here, a safe superset.
func quoteDuckDuckGoBangs(query string) string {
	parts := strings.Fields(query)
	for i, val := range parts {
		if strings.HasPrefix(val, "!") {
			parts[i] = "'" + val + "'"
		}
	}
	return strings.Join(parts, " ")
}

// queryNodes evaluates expr relative to node and returns the matching nodes.
// The expressions are static and validated at development time, so an
// evaluation error yields an empty result set (defensive, EC-011).
func queryNodes(node *html.Node, expr string) []*html.Node {
	nodes, err := htmlquery.QueryAll(node, expr)
	if err != nil {
		return nil
	}
	return nodes
}
