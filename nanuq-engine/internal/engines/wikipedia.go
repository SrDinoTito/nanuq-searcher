// wikipedia.go is a faithful Go port of SearXNG's
// example/searxng/searx/engines/wikipedia.py: it queries the MediaWiki
// REST v1 page-summary API (rest_v1_summary_url, wikipedia.py L93) and emits
// a main result and/or an infobox depending on the display_type override.
//
// The Wikipedia language is resolved from cfg.Overrides["language"] (default
// "en") or from params.Language; the wiki_netloc traits table of the Python
// (get_wiki_params, wikipedia.py L137-145) is not modeled in the Go config,
// so the netloc is derived as "{lang}.wikipedia.org" (documented deviation).
//
// NOTE (deviation from the task text): the task described the legacy
// MediaWiki search API (action=query&list=search&srsearch=...) with
// suggestions. The reference file uses the REST v1 summary API and produces
// NO suggestions — this port follows the reference file (task rule: "port
// fiel", no invention). The action=query/srsearch API belongs to
// engines/mediawiki.py, a different module.
package engines

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
	"nanuq-engine/internal/network"
	"nanuq-engine/internal/result"
)

// wikipediaUserAgent is a fixed browser-like User-Agent (documented
// deviation): wikipedia.py (L148-160) sets no request headers, so without
// this the Go engine layer would send the net/http default
// "Go-http-client/1.1", which the Wikipedia REST API rejects upstream
// ("access denied" / suspended engine). A static browser UA keeps the
// value consistent across requests without any HTTP round-trip, mirroring
// the ddgUserAgent approach in duckduckgo.go.
const wikipediaUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// wikipediaEngine implements engine.Engine. One instance is created per YAML
// engine entry (1:N pattern); the display slots are driven by the optional
// "display_type" override (wikipedia.py L77-80: "list" adds a hit to the
// result list, "infobox" shows a hit in the info box, both can be set).
type wikipediaEngine struct {
	cfg *config.EngineConfig

	displayList    bool   // "list" in display_type
	displayInfobox bool   // "infobox" in display_type (Python default)
	baseLang       string // override "language", default "en" (get_wiki_params L143)
}

// NewWikipediaEngine builds one Wikipedia engine instance per YAML entry.
// Unlike the data-driven xpath/json_engine modules there is no required
// override: the module contract of wikipedia.py works with the REST v1
// summary endpoint and only optional "display_type"/"language" overrides.
func NewWikipediaEngine(cfg *config.EngineConfig) (engine.Engine, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%w: wikipedia engine: nil config", engine.ErrInvalidConfig)
	}

	e := &wikipediaEngine{
		cfg:            cfg,
		displayInfobox: true, // wikipedia.py L77: display_type default ["infobox"]
		baseLang:       overrideStringDef(cfg.Overrides, "language", "en"),
	}

	// display_type override: single string, comma-separated string, or list
	// of strings; each entry must be "list" or "infobox" (EC-011: bad
	// overrides surface as construction errors, never as panics).
	if v, ok := cfg.Overrides["display_type"]; ok {
		list, infobox, err := parseDisplayType(v)
		if err != nil {
			return nil, fmt.Errorf("%w: wikipedia engine %q: %v", engine.ErrInvalidConfig, cfg.Name, err)
		}
		e.displayList = list
		e.displayInfobox = infobox
	}
	return e, nil
}

// parseDisplayType normalizes the display_type override value into the two
// display flags (wikipedia.py L77-80). An unknown entry is an error, so a
// typo cannot silently disable the engine.
func parseDisplayType(v any) (list, infobox bool, err error) {
	var parts []string
	switch t := v.(type) {
	case string:
		for _, p := range strings.Split(t, ",") {
			parts = append(parts, strings.TrimSpace(p))
		}
	case []any:
		for _, el := range t {
			if s, ok := el.(string); ok {
				parts = append(parts, s)
			}
		}
	default:
		return false, false, fmt.Errorf("invalid display_type %T, expected string or list", v)
	}
	for _, p := range parts {
		switch p {
		case "list":
			list = true
		case "infobox":
			infobox = true
		default:
			return false, false, fmt.Errorf("unsupported display_type entry %q", p)
		}
	}
	return list, infobox, nil
}

// Name returns the engine's configured name (YAML entry name).
func (e *wikipediaEngine) Name() string { return e.cfg.Name }

// Shortcut returns the configured shortcut.
func (e *wikipediaEngine) Shortcut() string { return e.cfg.Shortcut }

// Categories returns the configured categories.
func (e *wikipediaEngine) Categories() []string { return e.cfg.Categories }

// NeedsInit reports that no per-engine init is required (the traits fetch of
// wikipedia.py fetch_traits is a runtime network fetch, out of scope).
func (e *wikipediaEngine) NeedsInit() bool { return false }

// Setup is a no-op for the wikipedia engine.
func (e *wikipediaEngine) Setup(_ context.Context, _ *config.EngineConfig) error { return nil }

// Init is a no-op for the wikipedia engine.
func (e *wikipediaEngine) Init(_ context.Context) error { return nil }

// Request mutates params to build the REST v1 summary request — port of
// wikipedia.py request() (L148-160). It performs no I/O.
func (e *wikipediaEngine) Request(query string, params *engine.RequestParams) error {
	if params == nil {
		return errors.New("wikipedia engine: nil request params")
	}

	// wikipedia.py L150-151: `if query.islower(): query = query.title()`.
	if pythonIsLower(query) {
		query = pythonTitle(query)
	}

	// wikipedia.py L153-154: wiki_netloc = traits custom lookup keyed by the
	// resolved language, default "en.wikipedia.org". Port: the "language"
	// override wins, else the requested language's 2-letter prefix (region
	// variants like "zh-CN" collapse to the wiki language "zh"), else "en".
	lang := e.baseLang
	if params.Language != "" && params.Language != "all" {
		l := params.Language
		if len(l) >= 2 {
			l = l[:2]
		}
		lang = l
	}
	netloc := lang + ".wikipedia.org"

	// wikipedia.py L154: title = urllib.parse.quote(query). url.PathEscape
	// matches quote() for typical titles (space -> %20, letters/digits/-_.~
	// untouched) with one documented difference: '/' is escaped as %2F
	// (Python quote() leaves it as a path separator).
	title := url.PathEscape(query)

	// wikipedia.py L155: rest_v1_summary_url.format(wiki_netloc=..., title=...).
	params.URL = "https://" + netloc + "/api/rest_v1/page/summary/" + title
	params.Method = "GET"

	// wikipedia.py does not set request headers, so the Go engine layer would
	// send the net/http default UA ("Go-http-client/1.1"), which the REST API
	// rejects upstream (suspended / access denied). Set a fixed browser-like
	// User-Agent (documented deviation, mirrors ddgUserAgent in duckduckgo.go).
	headers := params.Headers
	if headers == nil {
		headers = make(http.Header)
		params.Headers = headers
	}
	headers.Set("User-Agent", wikipediaUserAgent)

	// wikipedia.py L157-158: params["raise_for_httperror"] = False and
	// params["soft_max_redirects"] = 2 have no RequestParams field (network
	// layer, TASK-007); the Response step below therefore handles the HTTP
	// statuses itself (404/400 short-circuits + raise_for_httperror).
	return nil
}

// Response converts an already-downloaded HTTP response into raw results —
// port of wikipedia.py response() (L164-210).
func (e *wikipediaEngine) Response(resp *http.Response) ([]*result.RawResult, error) {
	if resp == nil {
		return nil, errors.New("wikipedia engine: nil http response")
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("wikipedia engine %q: read response body: %w", e.cfg.Name, err)
	}

	// wikipedia.py L167-168: `if resp.status_code == 404: return []`.
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	// wikipedia.py L169-180: a 400 whose body is the HyperSwitch
	// bad_request/title-invalid-characters error also means "no results".
	if resp.StatusCode == http.StatusBadRequest {
		if api, err := decodeJSON(body); err == nil {
			if apiMap, ok := api.(map[string]any); ok {
				if apiMap["type"] == "https://mediawiki.org/wiki/HyperSwitch/errors/bad_request" &&
					apiMap["detail"] == "title-invalid-characters" {
					return nil, nil
				}
			}
		}
	}

	// wikipedia.py L181: _network.raise_for_httperror(resp) — ported to
	// network.RaiseForHTTPError, which maps 402/403/429 to suspension
	// decisions (TASK-007) and errors on any other non-2xx status.
	if err := network.RaiseForHTTPError(resp, body); err != nil {
		return nil, err
	}

	// wikipedia.py L183: api_result = resp.json().
	api, err := decodeJSON(body)
	if err != nil {
		return nil, fmt.Errorf("wikipedia engine %q: invalid JSON response: %w", e.cfg.Name, err)
	}
	apiMap, ok := api.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("wikipedia engine %q: JSON response is not an object", e.cfg.Name)
	}

	// wikipedia.py L184: title = html_to_text(titles.display or title).
	title := ""
	if v, ok := Query(apiMap, Parse("titles/display")); ok {
		title = toString(v)
	}
	if title == "" {
		title = toString(apiMap["title"])
	}
	title = htmlToText(title)

	// wikipedia.py L185: wikipedia_link = content_urls.desktop.page.
	link, ok := Query(apiMap, Parse("content_urls/desktop/page"))
	if !ok {
		return nil, fmt.Errorf("wikipedia engine %q: missing content_urls.desktop.page", e.cfg.Name)
	}
	wikipediaLink := toString(link)

	// wikipedia.py L186: `api_result.get("type") != "standard"` — anything
	// that cannot be rendered as an infobox (disambiguation pages, lists,
	// ...) falls back to the result list.
	isStandard := toString(apiMap["type"]) == "standard"

	results := make([]*result.RawResult, 0, 2)

	// wikipedia.py L187-196: 'list' display (or a non-standard page) emits a
	// main result whose content is the API description.
	if e.displayList || !isStandard {
		results = append(results, result.NewMain(&result.MainResult{
			URL:     wikipediaLink,
			Title:   title,
			Content: toString(apiMap["description"]),
		}))
	}

	// wikipedia.py L198-208: 'infobox' display for standard pages emits an
	// infobox with the extract and thumbnail. The Python "urls" list
	// ([{"title": "Wikipedia", "url": wikipedia_link}]) loses its label in
	// the Go Infobox model (URLs is []string, documented deviation).
	if e.displayInfobox && isStandard {
		imgSrc := ""
		if v, ok := Query(apiMap, Parse("thumbnail/source")); ok {
			imgSrc = toString(v)
		}
		results = append(results, result.NewInfobox(&result.Infobox{
			Title:   title,
			Content: toString(apiMap["extract"]),
			ImgSrc:  imgSrc,
			URLs:    []string{wikipediaLink},
		}))
	}
	return results, nil
}

// pythonIsLower mirrors str.islower() (wikipedia.py L150): at least one cased
// character and no uppercase character.
func pythonIsLower(s string) bool {
	hasCased := false
	for _, r := range s {
		if unicode.IsUpper(r) {
			return false
		}
		if unicode.IsLower(r) {
			hasCased = true
		}
	}
	return hasCased
}

// pythonTitle mirrors str.title() (wikipedia.py L151) using the CPython
// algorithm: an uncased character ends a word; the first cased character of a
// word is uppercased and the remaining cased characters are lowercased.
// "hello world" -> "Hello World", "hello-world" -> "Hello-World",
// "123 abc" -> "123 Abc".
func pythonTitle(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	prevCased := false
	for _, r := range s {
		cased := unicode.IsUpper(r) || unicode.IsLower(r)
		if cased {
			if !prevCased {
				r = unicode.ToUpper(r)
			} else {
				r = unicode.ToLower(r)
			}
		}
		sb.WriteRune(r)
		prevCased = cased
	}
	return sb.String()
}
