// Package engines implements the engine modules of nanuq-engine.
//
// This file is a faithful Go port of SearXNG's searx/engines/xpath.py: a
// data-driven "ghost" engine configured 100% through YAML overrides
// (search_url, results_xpath, url_xpath, ...) collected in
// config.EngineConfig.Overrides (see internal/config, DSG-003). The helpers
// extract_text / extract_url are ports of the homonymous searx/utils.py
// functions.
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
	"github.com/antchfx/xpath"
	"golang.org/x/net/html"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
	"nanuq-engine/internal/result"
)

// xpathEngine implements engine.Engine. One instance is created per YAML
// engine entry (1:N pattern); its behaviour is driven exclusively by the
// entry's Overrides map.
type xpathEngine struct {
	cfg *config.EngineConfig

	// Required settings (missing -> construction error).
	searchURL    string // search_url
	resultsXPath string // results_xpath

	// Optional XPath expressions, empty when not configured.
	urlXPath        string // url_xpath
	titleXPath      string // title_xpath
	contentXPath    string // content_xpath
	thumbnailXPath  string // thumbnail_xpath
	suggestionXPath string // suggestion_xpath

	// Request construction (xpath.py module defaults).
	method                 string            // method
	headers                http.Header       // headers
	cookies                []*http.Cookie    // cookies
	paging                 bool              // paging
	pageSize               int               // page_size
	firstPageNum           int               // first_page_num
	sendPageNumOnFirstPage bool              // send_page_num_on_first_page
	languageSupport        bool              // language_support
	langAll                string            // lang_all
	timeRangeURL           string            // time_range_url
	timeRangeMap           map[string]string // time_range_map
	safeSearchMap          map[int]string    // safe_search_map

	// Response handling.
	noResultForHTTPStatus []int // no_result_for_http_status
}

// RegisterXPath registra la factory del engine genérico XPath data-driven.
// TASK-011 (RegisterAll) invocará esta función junto con Register de json_engine.
//
// NOTE (colisión resuelta, Opción B): la especificación original de TASK-009
// pedía un Register package-level que registrara únicamente "xpath", pero
// TASK-010's json_engine.go ya declara un Register con el mismo nombre (Go
// prohíbe dos funcs homónimas en un mismo paquete). Resolución aprobada:
// esta función se renombra a RegisterXPath y registra SOLO "xpath"; el
// registro de "json_engine" ya lo hace el Register de json_engine.go.
func RegisterXPath(reg *engine.Registry) error {
	return reg.Register("xpath", NewXPathEngine)
}

// NewXPathEngine builds one xpath engine per YAML entry. It returns a clear
// error wrapping engine.ErrInvalidConfig when a required override is missing
// (search_url or results_xpath) or when a configured XPath expression does
// not compile (EC-011: never panic on bad overrides).
func NewXPathEngine(cfg *config.EngineConfig) (engine.Engine, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%w: xpath engine: nil config", engine.ErrInvalidConfig)
	}
	ov := cfg.Overrides

	searchURL, ok := overrideString(ov, "search_url")
	if !ok || strings.TrimSpace(searchURL) == "" {
		return nil, fmt.Errorf("%w: xpath engine %q: missing required override 'search_url'", engine.ErrInvalidConfig, cfg.Name)
	}
	resultsXPath, ok := overrideString(ov, "results_xpath")
	if !ok || strings.TrimSpace(resultsXPath) == "" {
		return nil, fmt.Errorf("%w: xpath engine %q: missing required override 'results_xpath'", engine.ErrInvalidConfig, cfg.Name)
	}

	e := &xpathEngine{
		cfg:                    cfg,
		searchURL:              searchURL,
		resultsXPath:           resultsXPath,
		urlXPath:               overrideStringDef(ov, "url_xpath", ""),
		titleXPath:             overrideStringDef(ov, "title_xpath", ""),
		contentXPath:           overrideStringDef(ov, "content_xpath", ""),
		thumbnailXPath:         overrideStringDef(ov, "thumbnail_xpath", ""),
		suggestionXPath:        overrideStringDef(ov, "suggestion_xpath", ""),
		method:                 overrideStringDef(ov, "method", "GET"),
		headers:                overrideHeaders(ov, "headers"),
		cookies:                overrideCookies(ov, "cookies"),
		paging:                 overrideBoolDef(ov, "paging", false),
		pageSize:               overrideIntDef(ov, "page_size", 1),
		firstPageNum:           overrideIntDef(ov, "first_page_num", 1),
		sendPageNumOnFirstPage: overrideBoolDef(ov, "send_page_num_on_first_page", true),
		languageSupport:        overrideBoolDef(ov, "language_support", true),
		langAll:                overrideStringDef(ov, "lang_all", "en"),
		timeRangeURL:           overrideStringDef(ov, "time_range_url", "&hours={time_range_val}"),
		timeRangeMap:           overrideStringMap(ov, "time_range_map"),
		safeSearchMap:          overrideIntKeyStringMap(ov, "safe_search_map"),
		noResultForHTTPStatus:  overrideIntSlice(ov, "no_result_for_http_status"),
	}

	// EC-011: a bad XPath override must surface as a clear construction
	// error, never as a panic at request time. Optional XPaths default to ""
	// (not configured) and are skipped — only the required results_xpath and
	// any non-empty optional override are compiled. Mirrors xpath.py, where
	// unset optional xpaths are left empty and never evaluated.
	for _, x := range []struct{ key, expr string }{
		{"results_xpath", e.resultsXPath},
		{"url_xpath", e.urlXPath},
		{"title_xpath", e.titleXPath},
		{"content_xpath", e.contentXPath},
		{"thumbnail_xpath", e.thumbnailXPath},
		{"suggestion_xpath", e.suggestionXPath},
	} {
		if strings.TrimSpace(x.expr) == "" {
			continue // optional XPath not configured
		}
		if _, err := xpath.Compile(x.expr); err != nil {
			return nil, fmt.Errorf("%w: xpath engine %q: invalid XPath for override %q: %v", engine.ErrInvalidConfig, cfg.Name, x.key, err)
		}
	}
	return e, nil
}

// Name returns the engine's configured name (YAML entry name).
func (e *xpathEngine) Name() string { return e.cfg.Name }

// Shortcut returns the configured shortcut.
func (e *xpathEngine) Shortcut() string { return e.cfg.Shortcut }

// Categories returns the configured categories.
func (e *xpathEngine) Categories() []string { return e.cfg.Categories }

// NeedsInit reports that no per-engine init is required.
func (e *xpathEngine) NeedsInit() bool { return false }

// Setup is a no-op for the xpath engine (all config comes from Overrides).
func (e *xpathEngine) Setup(_ context.Context, _ *config.EngineConfig) error { return nil }

// Init is a no-op for the xpath engine.
func (e *xpathEngine) Init(_ context.Context) error { return nil }

// Request mutates params to build the search request: it formats search_url
// by replacing its placeholders and applies method/headers/cookies from
// Overrides. It performs no I/O. Port of searx/engines/xpath.py request().
//
// Placeholders (mirroring xpath.py's fargs):
//   - {query}:      query URL-encoded (urlencode({'q': query})[2:])
//   - {lang}:       params.Language[:2] (lang_all default) when language_support
//   - {pageno}:     (pageno-1)*page_size+first_page_num when paging is on
//   - {time_range}: time_range_url with time_range_map[params.TimeRange]
//   - {safe_search}: safe_search_map[params.SafeSearch]
func (e *xpathEngine) Request(query string, params *engine.RequestParams) error {
	if params == nil {
		return errors.New("xpath engine: nil request params")
	}

	// {query}: xpath.py uses urlencode({'q': query})[2:]; Encode() sorts the
	// single key and escapes with '+' for spaces, exactly like quote_plus.
	encodedQuery := url.Values{"q": []string{query}}.Encode()[2:]

	// {lang}: xpath.py: lang = lang_all; if params[language] != 'all':
	// lang = params[language][:2]. When the engine has no language support
	// the placeholder is removed from the URL.
	lang := e.langAll
	if params.Language != "" && params.Language != "all" && e.languageSupport {
		if len(params.Language) >= 2 {
			lang = params.Language[:2]
		} else {
			lang = params.Language
		}
	} else if !e.languageSupport {
		lang = ""
	}

	// {pageno}: xpath.py: if send_page_num_on_first_page or pageno != 1:
	// pageno = (pageno - 1) * page_size + first_page_num.
	pageno := ""
	if e.paging && (e.sendPageNumOnFirstPage || params.Pageno != 1) {
		pageno = strconv.Itoa((params.Pageno-1)*e.pageSize + e.firstPageNum)
	}

	// {time_range}: the search processor blanks params.TimeRange for engines
	// without time_range_support, so a map hit implies support.
	timeRange := ""
	if v, ok := e.timeRangeMap[params.TimeRange]; ok {
		timeRange = strings.ReplaceAll(e.timeRangeURL, "{time_range_val}", v)
	}

	// {safe_search}: mapped value for params.SafeSearch, empty when absent.
	safeSearch := ""
	if v, ok := e.safeSearchMap[params.SafeSearch]; ok {
		safeSearch = v
	}

	r := strings.NewReplacer(
		"{query}", encodedQuery,
		"{lang}", lang,
		"{pageno}", pageno,
		"{time_range}", timeRange,
		"{safe_search}", safeSearch,
	)
	params.URL = r.Replace(e.searchURL)

	// xpath.py: params[method] = method; params[headers].update(headers);
	// params[cookies].update(cookies).
	params.Method = e.method
	if len(e.headers) > 0 {
		if params.Headers == nil {
			params.Headers = e.headers.Clone()
		} else {
			for k, vals := range e.headers {
				for _, v := range vals {
					params.Headers.Add(k, v)
				}
			}
		}
	}
	params.Cookies = append(params.Cookies, e.cookies...)
	return nil
}

// Response parses the HTML body, evaluates the configured XPath expressions
// and returns the extracted results. Port of searx/engines/xpath.py
// response().
func (e *xpathEngine) Response(resp *http.Response) ([]*result.RawResult, error) {
	if resp == nil {
		return nil, errors.New("xpath engine: nil http response")
	}

	// xpath.py: if no_result_for_http_status and status in it: return [].
	if intInSlice(resp.StatusCode, e.noResultForHTTPStatus) {
		return nil, nil
	}

	// xpath.py: dom = html.fromstring(resp.text).
	doc, err := htmlquery.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("xpath engine %q: parse response body: %w", e.cfg.Name, err)
	}

	results := []*result.RawResult{}

	// xpath.py: for result in eval_xpath_list(dom, results_xpath): ...
	resultNodes, err := htmlquery.QueryAll(doc, e.resultsXPath)
	if err != nil {
		return nil, fmt.Errorf("xpath engine %q: evaluate results_xpath: %w", e.cfg.Name, err)
	}
	for _, node := range resultNodes {
		// url_xpath is required per result; a missing/empty URL skips the
		// result (xpath.py raises ValueError('URL not found') and the
		// exception aborts that result).
		rawURL, err := e.extractURLFromNode(node, e.urlXPath)
		if err != nil || rawURL == "" {
			continue
		}
		m := &result.MainResult{
			URL:     rawURL,
			Title:   extractTextFromNode(node, e.titleXPath),
			Content: extractTextFromNode(node, e.contentXPath),
		}
		// xpath.py: if thumbnail_xpath: thumb = eval_xpath_list(...);
		// if len(thumb) > 0: tmp['thumbnail'] = extract_url(thumb, search_url).
		if e.thumbnailXPath != "" {
			if thumb, terr := e.extractURLFromNode(node, e.thumbnailXPath); terr == nil {
				m.Thumbnail = thumb
			}
		}
		results = append(results, result.NewMain(m))
	}

	// xpath.py: if suggestion_xpath: for suggestion in eval_xpath(dom, ...):
	// append({'suggestion': extract_text(suggestion)}).
	if e.suggestionXPath != "" {
		suggNodes, err := htmlquery.QueryAll(doc, e.suggestionXPath)
		if err != nil {
			return nil, fmt.Errorf("xpath engine %q: evaluate suggestion_xpath: %w", e.cfg.Name, err)
		}
		for _, sn := range suggNodes {
			if text := extractTextFromNode(sn, "."); text != "" {
				results = append(results, result.NewSuggestion(text))
			}
		}
	}

	slog.Debug("xpath engine: extracted results", "engine", e.cfg.Name, "count", len(results))
	return results, nil
}

// extractTextFromNode evaluates expr relative to node and returns the
// collapsed text of every match, or "" when expr is empty or matches nothing.
func extractTextFromNode(node *html.Node, expr string) string {
	if expr == "" {
		return ""
	}
	nodes, err := htmlquery.QueryAll(node, expr)
	if err != nil {
		// Pre-validated at construction; keep evaluation defensive (EC-011).
		return ""
	}
	return extractText(nodes)
}

// extractURLFromNode evaluates expr relative to node and returns the
// normalized absolute URL of the match, resolving it against the engine's
// search_url (xpath.py: extract_url(result, search_url)).
func (e *xpathEngine) extractURLFromNode(node *html.Node, expr string) (string, error) {
	nodes, err := htmlquery.QueryAll(node, expr)
	if err != nil {
		return "", err
	}
	return extractURL(nodes, e.searchURL)
}

// extractText concatenates the text of the selected nodes and collapses all
// whitespace runs into single spaces. Port of searx/utils.py extract_text
// (element path: tostring(method='text') then ' '.join(text.split())).
func extractText(nodes []*html.Node) string {
	var sb strings.Builder
	for _, n := range nodes {
		if n == nil {
			continue
		}
		// InnerText also yields attribute values for attribute nodes (e.g.
		// //a/@href), matching the way xpath.py evaluates url_xpath.
		sb.WriteString(collapseWhitespace(htmlquery.InnerText(n)))
	}
	return strings.TrimSpace(sb.String())
}

// collapseWhitespace replaces every whitespace run with a single space.
func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// extractURL returns the normalized absolute URL from the selected nodes,
// resolving relative URLs against baseURL. Port of searx/utils.py
// extract_url + normalize_url.
func extractURL(nodes []*html.Node, baseURL string) (string, error) {
	if len(nodes) == 0 {
		return "", errEmptyURLResultSet
	}
	raw := extractText(nodes)
	if raw == "" {
		return "", errURLNotFound
	}
	return normalizeURL(raw, baseURL)
}

// normalizeURL mirrors searx/utils.py normalize_url:
//   - "//host/path" gains the base scheme (http when base has none) + ":"
//   - "/path" is resolved against baseURL (urljoin)
//   - any URL without "://" is resolved against baseURL (urljoin)
//   - the result must have a host; a missing path appends "/".
func normalizeURL(raw, baseURL string) (string, error) {
	base, baseErr := url.Parse(baseURL)

	if strings.HasPrefix(raw, "//") {
		scheme := "http"
		if baseErr == nil && base.Scheme != "" {
			scheme = base.Scheme
		}
		raw = scheme + ":" + raw
	} else if strings.HasPrefix(raw, "/") {
		ref, err := url.Parse(raw)
		if err != nil {
			return "", err
		}
		if baseErr != nil {
			return "", baseErr
		}
		raw = base.ResolveReference(ref).String()
	}
	if !strings.Contains(raw, "://") {
		ref, err := url.Parse(raw)
		if err != nil {
			return "", err
		}
		if baseErr != nil {
			return "", baseErr
		}
		raw = base.ResolveReference(ref).String()
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errCannotParseURL, err)
	}
	if parsed.Host == "" {
		return "", errCannotParseURL
	}
	if parsed.Path == "" {
		raw += "/"
	}
	return raw, nil
}

// Sentinel errors mirroring the ValueError cases of searx/utils.py.
var (
	errEmptyURLResultSet = errors.New("empty url resultset")
	errURLNotFound       = errors.New("url not found")
	errCannotParseURL    = errors.New("cannot parse url")
)

// scalarToString converts a YAML-decoded scalar (yaml.v3 decodes ints to
// int, floats to float64, bools to bool) to its string form.
func scalarToString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case int:
		return strconv.Itoa(t), true
	case int64:
		return strconv.FormatInt(t, 10), true
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64), true
	case bool:
		return strconv.FormatBool(t), true
	default:
		return "", false
	}
}

func overrideString(ov map[string]any, key string) (string, bool) {
	v, ok := ov[key]
	if !ok {
		return "", false
	}
	return scalarToString(v)
}

func overrideStringDef(ov map[string]any, key, def string) string {
	if s, ok := overrideString(ov, key); ok {
		return s
	}
	return def
}

func overrideBoolDef(ov map[string]any, key string, def bool) bool {
	if v, ok := ov[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

func overrideIntDef(ov map[string]any, key string, def int) int {
	if v, ok := ov[key]; ok {
		switch t := v.(type) {
		case int:
			return t
		case int64:
			return int(t)
		case float64:
			return int(t)
		case string:
			if i, err := strconv.Atoi(t); err == nil {
				return i
			}
		}
	}
	return def
}

// overrideHeaders decodes a map[string]any into http.Header.
func overrideHeaders(ov map[string]any, key string) http.Header {
	m, ok := ov[key].(map[string]any)
	if !ok {
		return nil
	}
	h := make(http.Header, len(m))
	for k, v := range m {
		if s, ok := scalarToString(v); ok {
			h.Add(k, s)
		}
	}
	return h
}

// overrideCookies decodes a map[string]any into []*http.Cookie.
func overrideCookies(ov map[string]any, key string) []*http.Cookie {
	m, ok := ov[key].(map[string]any)
	if !ok {
		return nil
	}
	cs := make([]*http.Cookie, 0, len(m))
	for k, v := range m {
		if s, ok := scalarToString(v); ok {
			cs = append(cs, &http.Cookie{Name: k, Value: s})
		}
	}
	return cs
}

// overrideStringMap decodes a map[string]any of scalars (e.g. time_range_map).
func overrideStringMap(ov map[string]any, key string) map[string]string {
	m, ok := ov[key].(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if s, ok := scalarToString(v); ok {
			out[k] = s
		}
	}
	return out
}

// overrideIntKeyStringMap decodes a map[string]any whose keys are numeric
// strings (e.g. safe_search_map {0: ..., 1: ..., 2: ...}).
func overrideIntKeyStringMap(ov map[string]any, key string) map[int]string {
	m, ok := ov[key].(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[int]string, len(m))
	for k, v := range m {
		i, err := strconv.Atoi(k)
		if err != nil {
			continue
		}
		if s, ok := scalarToString(v); ok {
			out[i] = s
		}
	}
	return out
}

// overrideIntSlice decodes a []any of scalars (e.g. no_result_for_http_status).
func overrideIntSlice(ov map[string]any, key string) []int {
	list, ok := ov[key].([]any)
	if !ok {
		return nil
	}
	out := make([]int, 0, len(list))
	for _, v := range list {
		switch t := v.(type) {
		case int:
			out = append(out, t)
		case int64:
			out = append(out, int(t))
		case float64:
			out = append(out, int(t))
		case string:
			if i, err := strconv.Atoi(t); err == nil {
				out = append(out, i)
			}
		}
	}
	return out
}

func intInSlice(n int, s []int) bool {
	for _, v := range s {
		if v == n {
			return true
		}
	}
	return false
}
