// startpage.go implements the Startpage engine for nanuq-engine.
//
// It is a faithful Go port of example/searxng/searx/engines/startpage.py
// (with the startpage_news.py and startpage_images.py result branches, which
// that module exposes through the startpage_categ module constant). The module
// follows the 1:N pattern (DSG-003, REQ-004): one Go module serves every
// instance, discriminated from EngineConfig. The Python source splits this
// behaviour across three files (startpage.py / startpage_news.py /
// startpage_images.py) that share the code below; here the result branch is
// selected with the "startpage_categ" override ("web" default, or "news" /
// "images"). There is deliberately no Register function — wiring happens
// through the exported constructor.
//
// Documented deviations from the Python modules (all required by the Go
// engine contract, REQ-005, or out of scope):
//
//   - get_sc_code() (startpage.py L163-226) is NOT ported: it keeps a
//     persistent SQLite cache and performs an HTTP round-trip to the
//     homepage inside request(), which the Go contract forbids (Request is a
//     pure mutation of params). The "sc" form field is still emitted with an
//     empty value so the request shape matches Startpage's expectations.
//   - fetch_traits / traits.get_language / traits.get_region
//     (startpage.py L427-541) are not ported: the traits/babel machinery is
//     out of scope for this engine. The language is derived from
//     params.Language (language part, default "en", matching the Python
//     fallback) and the "search_results_region" cookie key is omitted.
//   - remove_pua_from_str (startpage_news.py) is not ported: private-use-area
//     codepoint stripping is a text-cleaning detail with no Go equivalent in
//     the result model.
//   - The published date is parsed with time.Parse("2 Jan 2006") instead of
//     dateutil.parser: only the day-first "dd MMM yyyy" and "N days ago"
//     branches the Python code implements via regex are reachable, so no
//     dateutil dependency is needed. The parsed date has no Go field on
//     MainResult and is dropped.
//   - Python raises KeyError (engine-wide failure) when "clickUrl" is
//     missing; Go defensively skips web/news results without a usable URL and
//     keeps the rest (the Python image branch already skips None results).
//   - Content-Type is NOT set in params.Headers: the Go network layer
//     (internal/network buildRequest) already sets it automatically when
//     Data is non-nil (same rationale as duckduckgo.py L451).
//   - Captcha detection: the Location-header check of response() (startpage.py
//     L410-411) is ported and surfaces engine.EngineSuspendError. The
//     redirect-based detection inside get_sc_code() is not ported (see above).
package engines

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
	"nanuq-engine/internal/result"
)

const (
	startpageBaseURL   = "https://www.startpage.com"
	startpageSearchURL = startpageBaseURL + "/sp/search"

	startpageCategWeb    = "web"
	startpageCategNews   = "news"
	startpageCategImages = "images"
)

var (
	startpageTimeRangeDict = map[string]string{
		"day":   "d",
		"week":  "w",
		"month": "m",
		"year":  "y",
	}

	startpageSafeSearchDict = map[int]string{
		0: "none",
		1: "moderate",
		2: "heavy",
	}

	// startpageRePublishedDate matches the day-first date prefix
	// "^([1-9]|[1-2][0-9]|3[0-1]) [A-Z][a-z]{2} [0-9]{4} \.\.\. ".
	startpageRePublishedDate = regexp.MustCompile(`^([1-9]|[1-2][0-9]|3[0-1]) [A-Z][a-z]{2} [0-9]{4} \.\.\. `)

	// startpageReDaysAgo matches "^[0-9]+ days? ago \.\.\. ".
	startpageReDaysAgo = regexp.MustCompile(`^[0-9]+ days? ago \.\.\. `)
)

// startpageEngine implements engine.Engine for the Startpage frontend.
type startpageEngine struct {
	cfg    *config.EngineConfig
	categ  string
	logger *slog.Logger
}

// NewStartpageEngine builds a Startpage engine. The optional override
// "startpage_categ" selects the result branch: "web" (default, port of
// startpage.py), "news" (startpage_news.py) or "images"
// (startpage_images.py).
func NewStartpageEngine(cfg *config.EngineConfig) (engine.Engine, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%w: startpage engine: nil config", engine.ErrInvalidConfig)
	}

	categ := overrideStringDef(cfg.Overrides, "startpage_categ", startpageCategWeb)
	switch categ {
	case startpageCategWeb, startpageCategNews, startpageCategImages:
	default:
		return nil, fmt.Errorf("%w: startpage engine: unknown startpage_categ %q", engine.ErrInvalidConfig, categ)
	}

	return &startpageEngine{cfg: cfg, categ: categ, logger: slog.Default()}, nil
}

func (e *startpageEngine) Name() string         { return e.cfg.Name }
func (e *startpageEngine) Shortcut() string     { return e.cfg.Shortcut }
func (e *startpageEngine) Categories() []string { return e.cfg.Categories }
func (e *startpageEngine) NeedsInit() bool      { return false }

func (e *startpageEngine) Setup(_ context.Context, _ *config.EngineConfig) error {
	return nil
}

func (e *startpageEngine) Init(_ context.Context) error {
	return nil
}

// Request mutates params to issue the Startpage search form POST. It never
// performs I/O (REQ-005): the "sc" token that startpage.py scrapes from the
// homepage inside request() is emitted empty (see package doc).
func (e *startpageEngine) Request(query string, params *engine.RequestParams) error {
	if params == nil {
		return errors.New("startpage engine: nil request params")
	}

	if params.Headers == nil {
		params.Headers = make(http.Header)
	}
	params.Headers.Set("Origin", startpageBaseURL)
	params.Headers.Set("Referer", startpageBaseURL+"/")

	safesearch := startpageSafeSearchDict[params.SafeSearch]
	if safesearch == "" {
		safesearch = "none"
	}

	args := map[string]string{
		"query":     query,
		"cat":       e.categ,
		"t":         "device",
		"sc":        "",
		"with_date": startpageTimeRangeDict[params.TimeRange],
		"abd":       "1",
		"abe":       "1",
		"qsr":       "all",
		"qadf":      safesearch,
	}

	// traits.get_language(searxng_locale, "en") always yields a language;
	// params.Language is the Go contract equivalent, defaulting to "en".
	lang := params.Language
	if lang == "" {
		lang = "en"
	}
	if i := strings.Index(lang, "-"); i > 0 {
		lang = lang[:i]
	}
	args["language"] = lang
	args["lui"] = lang

	if params.Pageno > 1 {
		args["page"] = strconv.Itoa(params.Pageno)
		args["segment"] = "startpage.udog"
	}

	params.Cookies = append(params.Cookies, &http.Cookie{
		Name:  "preferences",
		Value: e.preferencesCookie(safesearch, lang),
	})

	params.Method = http.MethodPost
	params.URL = startpageSearchURL
	params.Data = args
	return nil
}

// preferencesCookie builds the "preferences" cookie value. The key order is
// significant: startpage.py builds an OrderedDict and joins
// "N1N".join(["%sEEE%s" % x for x in cookie.items()]).
func (e *startpageEngine) preferencesCookie(safesearch, lang string) string {
	parts := []string{
		"date_timeEEEworld",
		"disable_family_filterEEE" + safesearch,
		"disable_open_in_new_windowEEE0",
		"enable_post_methodEEE1",
		"enable_proxy_safety_suggestEEE1",
		"enable_stay_controlEEE1",
		"instant_answersEEE1",
		"lang_homepageEEEs/device/en/",
		"num_of_resultsEEE10",
		"suggestionsEEE1",
		"wt_unitEEEcelsius",
	}
	if lang != "" {
		parts = append(parts, "languageEEE"+lang, "language_uiEEE"+lang)
	}
	return strings.Join(parts, "N1N")
}

// Response parses the Startpage search results page. The Python module
// extracts the serialized results out of the embedded React props instead of
// scraping HTML, and this port does the same.
func (e *startpageEngine) Response(resp *http.Response) ([]*result.RawResult, error) {
	if resp == nil {
		return nil, errors.New("startpage engine: nil http response")
	}

	// startpage.py L410-411: a redirect to the captcha page aborts the
	// request before any parsing.
	if loc := resp.Header.Get("Location"); strings.HasPrefix(loc, "https://www.startpage.com/sp/captcha") {
		return nil, &engine.EngineSuspendError{
			Reason:     "CAPTCHA (startpage): blocked by captcha redirect",
			SuspendFor: 0,
		}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("startpage engine: read response body: %w", err)
	}

	categ := capitalize(e.categ)
	resultsRaw := "{" + extrValue(string(body), "React.createElement(UIStartpage.AppSerp"+categ+", {", "}}") + "}}"
	if resultsRaw == "{}" {
		return nil, nil
	}

	resultsJSON, err := decodeJSON([]byte(resultsRaw))
	if err != nil {
		return nil, fmt.Errorf("startpage engine: decode results json: %w", err)
	}

	regions := startpageRegions(resultsJSON)
	if regions == nil {
		return nil, nil
	}

	results := make([]*result.RawResult, 0, 8)
	for _, resultsCateg := range startpageMainline(regions) {
		for _, item := range startpageResults(resultsCateg) {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			displayType := toString(m["display_type"])
			switch {
			case displayType == "web-google":
				if r := startpageWebResult(m); r != nil {
					results = append(results, r)
				}
			case displayType == "news-bing":
				if r := startpageNewsResult(m); r != nil {
					results = append(results, r)
				}
			case strings.Contains(displayType, "images"):
				if r := startpageImageResult(m); r != nil {
					results = append(results, r)
				}
			}
		}
	}
	return results, nil
}

// startpageWebResult ports _get_web_result (startpage.py L331-340).
func startpageWebResult(item map[string]any) *result.RawResult {
	url := toString(item["clickUrl"])
	if url == "" {
		return nil
	}
	content := htmlToText(toString(item["description"]))
	content, _ = startpageParsePublishedDate(content)
	return result.NewMain(&result.MainResult{
		URL:     url,
		Title:   htmlToText(toString(item["title"])),
		Content: content,
	})
}

// startpageNewsResult ports _get_news_result (startpage.py L343-365); the
// published date and PUA stripping have no Go MainResult equivalent.
func startpageNewsResult(item map[string]any) *result.RawResult {
	url := toString(item["clickUrl"])
	if url == "" {
		return nil
	}
	m := &result.MainResult{
		URL:     url,
		Title:   htmlToText(toString(item["title"])),
		Content: htmlToText(toString(item["description"])),
	}
	if thumb := toString(item["thumbnailUrl"]); thumb != "" {
		m.Thumbnail = startpageBaseURL + thumb
	}
	return result.NewMain(m)
}

// startpageImageResult ports _get_image_result (startpage.py L368-399). The
// Python "url" (altClickUrl) has no Go Image field and is dropped.
func startpageImageResult(item map[string]any) *result.RawResult {
	url := toString(item["altClickUrl"])
	if url == "" {
		return nil
	}
	img := &result.Image{}
	if thumb := toString(item["thumbnailUrl"]); thumb != "" {
		img.ThumbnailSrc = startpageBaseURL + thumb
	}
	// width/height come from the decoded JSON (json.Number): the shared
	// intValue helper does not handle json.Number, so parse via toString.
	if w, err := strconv.Atoi(toString(item["width"])); err == nil && w > 0 {
		if h, err := strconv.Atoi(toString(item["height"])); err == nil && h > 0 {
			img.Resolution = strconv.Itoa(w) + "x" + strconv.Itoa(h)
		}
	}
	if format := strings.ToUpper(toString(item["format"])); format != "" && format != "UNKNOWN" {
		img.ImgFormat = format
	}
	return result.NewImage(img)
}

// startpageParsePublishedDate ports _parse_published_date
// (startpage.py L302-328). Both branches strip the leading date token from
// the content; the parsed date is not returned (no Go field for it).
func startpageParsePublishedDate(content string) (string, time.Time) {
	var published time.Time
	if m := startpageRePublishedDate.FindString(content); m != "" {
		idx := strings.Index(content, "...")
		dateString := content[:idx-1]
		published, _ = time.Parse("2 Jan 2006", dateString)
		content = content[idx+4:]
	} else if m := startpageReDaysAgo.FindString(content); m != "" {
		idx := strings.Index(content, "...")
		dateString := content[:idx-1]
		days, _ := strconv.Atoi(dateString)
		published = time.Now().AddDate(0, 0, -days)
		content = content[idx+4:]
	}
	return content, published
}

// startpageRegions navigates results_json -> render -> presenter -> regions.
func startpageRegions(root any) map[string]any {
	render, ok := root.(map[string]any)
	if !ok {
		return nil
	}
	presenter, ok := render["render"].(map[string]any)
	if !ok {
		return nil
	}
	regions, ok := presenter["presenter"].(map[string]any)
	if !ok {
		return nil
	}
	out, _ := regions["regions"].(map[string]any)
	return out
}

// startpageMainline returns regions["mainline"] as a slice.
func startpageMainline(regions map[string]any) []any {
	mainline, _ := regions["mainline"].([]any)
	return mainline
}

// startpageResults returns the "results" list of one mainline entry.
func startpageResults(resultsCateg any) []any {
	m, _ := resultsCateg.(map[string]any)
	results, _ := m["results"].([]any)
	return results
}

// extrValue ports searx/utils.py extr(): the substring of text between the
// first occurrence of start and the first occurrence of end after it ("" when
// either marker is missing).
func extrValue(text, start, end string) string {
	pos := strings.Index(text, start)
	if pos < 0 {
		return ""
	}
	pos += len(start)
	pos2 := strings.Index(text[pos:], end)
	if pos2 < 0 {
		return ""
	}
	return text[pos : pos+pos2]
}

// capitalize ports Python str.capitalize() for the AppSerp component name
// ("web" -> "Web").
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + strings.ToLower(s[1:])
}
