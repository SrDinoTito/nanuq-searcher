// Package engines implements the search engine modules (DSG-003, REQ-004).
//
// This file is the generic data-driven JSON engine — a port of SearXNG's
// example/searxng/searx/engines/json_engine.py (CON-005), configured entirely
// through EngineConfig.Overrides: no module code per site. The mini JSON-path
// language it uses lives in jsonpath.go (CON-004 / DECISION-007: hand-ported,
// no JSONPath/JMESPath library).
package engines

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
	"nanuq-engine/internal/result"
)

// Register wires the "json_engine" module factory into reg (REQ-004). It is
// invoked from cmd/nanuq-server/main.go in TASK-011.
func Register(reg *engine.Registry) error {
	return reg.Register("json_engine", NewJSONEngine)
}

// jsonEngine is the generic data-driven JSON engine — a port of
// json_engine.py. All attributes come from EngineConfig.Overrides; the JSON
// path tokens are pre-parsed once at construction (jsonpath.go Parse).
type jsonEngine struct {
	logger     *slog.Logger
	name       string
	shortcut   string
	categories []string

	searchURL string
	method    string
	headers   map[string]string
	cookies   map[string]string

	resultsQuery    string
	urlQuery        string
	titleQuery      string
	contentQuery    string
	thumbnailQuery  string
	suggestionQuery string

	urlPrefix       string
	thumbnailPrefix string

	pageSize               int
	firstPageNum           int
	sendPageNumOnFirstPage bool
	langAll                string

	titleHTMLToText   bool
	contentHTMLToText bool

	// Pre-parsed JSON paths (jsonpath.go Parse).
	resultsTokens    []string
	urlTokens        []string
	titleTokens      []string
	contentTokens    []string
	thumbnailTokens  []string
	suggestionTokens []string
}

// NewJSONEngine builds a json_engine instance from one YAML entry (REQ-004,
// DECISION-005). The module contract (TASK-010) requires search_url and
// results_query; their absence is reported wrapping engine.ErrInvalidConfig.
//
// Deviation from json_engine.py: results_query is REQUIRED here — the Python
// default ” falls back to using the whole document, which TASK-010 drops.
func NewJSONEngine(cfg *config.EngineConfig) (engine.Engine, error) {
	searchURL, _ := strValue(cfg.Overrides, "search_url")
	if searchURL == "" {
		return nil, fmt.Errorf("%w: json_engine %q: missing required attribute search_url",
			engine.ErrInvalidConfig, cfg.Name)
	}
	resultsQuery, _ := strValue(cfg.Overrides, "results_query")
	if resultsQuery == "" {
		return nil, fmt.Errorf("%w: json_engine %q: missing required attribute results_query",
			engine.ErrInvalidConfig, cfg.Name)
	}

	e := &jsonEngine{
		logger:     slog.Default(),
		name:       cfg.Name,
		shortcut:   cfg.Shortcut,
		categories: []string(cfg.Categories),

		searchURL: searchURL,
		method:    strValueOr(cfg.Overrides, "method", "GET"),
		headers:   strMap(cfg.Overrides, "headers"),
		cookies:   strMap(cfg.Overrides, "cookies"),

		resultsQuery:    resultsQuery,
		urlQuery:        strValueOr(cfg.Overrides, "url_query", ""),
		titleQuery:      strValueOr(cfg.Overrides, "title_query", ""),
		contentQuery:    strValueOr(cfg.Overrides, "content_query", ""),
		thumbnailQuery:  strValueOr(cfg.Overrides, "thumbnail_query", ""),
		suggestionQuery: strValueOr(cfg.Overrides, "suggestion_query", ""),

		urlPrefix:       strValueOr(cfg.Overrides, "url_prefix", ""),
		thumbnailPrefix: strValueOr(cfg.Overrides, "thumbnail_prefix", ""),

		pageSize:               intValue(cfg.Overrides, "page_size", 1),
		firstPageNum:           intValue(cfg.Overrides, "first_page_num", 1),
		sendPageNumOnFirstPage: boolValue(cfg.Overrides, "send_page_num_on_first_page", true),
		langAll:                strValueOr(cfg.Overrides, "lang_all", "en"),

		titleHTMLToText:   boolValue(cfg.Overrides, "title_html_to_text", false),
		contentHTMLToText: boolValue(cfg.Overrides, "content_html_to_text", false),
	}

	e.resultsTokens = Parse(e.resultsQuery)
	e.urlTokens = Parse(e.urlQuery)
	e.titleTokens = Parse(e.titleQuery)
	e.contentTokens = Parse(e.contentQuery)
	e.thumbnailTokens = Parse(e.thumbnailQuery)
	e.suggestionTokens = Parse(e.suggestionQuery)

	return e, nil
}

func (e *jsonEngine) Name() string         { return e.name }
func (e *jsonEngine) Shortcut() string     { return e.shortcut }
func (e *jsonEngine) Categories() []string { return e.categories }
func (e *jsonEngine) NeedsInit() bool      { return false }

func (e *jsonEngine) Setup(context.Context, *config.EngineConfig) error { return nil }
func (e *jsonEngine) Init(context.Context) error                        { return nil }

// Request builds the outgoing request into params — a pure mutation with no
// I/O, port of json_engine.py request() (L318-358).
//
// Replacement placeholders: {query} (URL-escaped, urlencode({'q': q})[2:] is
// exactly url.QueryEscape), {lang}, {pageno}, {time_range} and {safe_search}
// (the latter two are empty: time-range/safe-search support is out of scope
// for TASK-010). Config headers and cookies are merged into params and
// params.Method is set from the config method (default GET).
//
// Deviations (justified, see TASK-010 report): request_body is not ported
// (no POST body support yet); url_query is NOT appended to the URL — in
// SearXNG url_query is a response-side JSON path (e.g. "mdn_url"), never a
// request URL parameter, and appending it would corrupt search_url.
func (e *jsonEngine) Request(query string, params *engine.RequestParams) error {
	// json_engine.py L320-322: lang = lang_all unless a concrete language is
	// requested.
	lang := e.langAll
	if params.Language != "" && params.Language != "all" {
		lang = params.Language
		if len(lang) > 2 {
			lang = lang[:2]
		}
	}

	// json_engine.py L333-335: pageno is empty on the first page unless
	// send_page_num_on_first_page, otherwise the 1-based page offset.
	pageno := ""
	if e.sendPageNumOnFirstPage || params.Pageno != 1 {
		pageno = strconv.Itoa((params.Pageno-1)*e.pageSize + e.firstPageNum)
	}

	// json_engine.py L337: urlencode({'q': query})[2:] == url.QueryEscape
	// (space -> '+', '&' -> '%26', '/' -> '%2F').
	repl := strings.NewReplacer(
		"{query}", url.QueryEscape(query),
		"{lang}", lang,
		"{pageno}", pageno,
		"{time_range}", "",
		"{safe_search}", "",
	)
	params.URL = repl.Replace(e.searchURL)
	params.Method = e.method

	// json_engine.py L344-345: params['headers'].update(headers) — config
	// headers override pre-existing params headers.
	if len(e.headers) > 0 {
		if params.Headers == nil {
			params.Headers = make(http.Header)
		}
		for k, v := range e.headers {
			params.Headers.Set(k, v)
		}
	}

	// json_engine.py L344: params['cookies'].update(cookies) — the Go
	// RequestParams carries cookies as a slice, so config cookies are
	// appended.
	for name, value := range e.cookies {
		params.Cookies = append(params.Cookies, &http.Cookie{Name: name, Value: value})
	}
	return nil
}

// Response converts an already-downloaded HTTP response into raw results —
// port of json_engine.py response() (L396-433) and extract_response_info()
// (L365-393). The network layer has already raised on non-2xx statuses, so no
// raise_for_httperror equivalent is needed here.
func (e *jsonEngine) Response(resp *http.Response) ([]*result.RawResult, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("json_engine %q: read response body: %w", e.name, err)
	}

	// json_engine.py L405-406: `if not resp.text: return []`.
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}

	data, err := decodeJSON(body)
	if err != nil {
		e.logger.Warn("json_engine: invalid JSON response",
			"engine", e.name, "error", err)
		return nil, fmt.Errorf("json_engine %q: invalid JSON response: %w", e.name, err)
	}

	// json_engine.py L411-417: rs = query(json, results_query); the Python
	// whole-document fallback (results_query == '') is dropped because
	// results_query is required (module contract).
	resultsData, ok := Query(data, e.resultsTokens)
	if !ok {
		return nil, nil
	}

	// TASK-010: a []any results set iterates each element; a map yields a
	// single result (Python iterates dict keys, which is broken — deviation
	// per task text).
	var items []any
	switch rd := resultsData.(type) {
	case []any:
		items = rd
	case map[string]any:
		items = []any{rd}
	default:
		return nil, nil
	}

	out := make([]*result.RawResult, 0, len(items))
	for _, item := range items {
		m := e.extractResponseInfo(item)
		if m == nil {
			continue // json_engine.py L421-422: skip unparsable items
		}
		out = append(out, result.NewMain(m))
	}

	// json_engine.py L429-432: suggestion_query is optional; every match
	// becomes a suggestion result.
	if len(e.suggestionTokens) > 0 {
		if suggestionData, ok := Query(data, e.suggestionTokens); ok {
			switch s := suggestionData.(type) {
			case []any:
				for _, el := range s {
					out = append(out, result.NewSuggestion(toString(el)))
				}
			default:
				out = append(out, result.NewSuggestion(toString(suggestionData)))
			}
		}
	}
	return out, nil
}

// extractResponseInfo maps one result item into a MainResult — port of
// json_engine.py extract_response_info() (L365-393). url and title are
// required (a failure to extract either returns nil and the item is skipped);
// content and thumbnail are best-effort.
func (e *jsonEngine) extractResponseInfo(item any) *result.MainResult {
	// json_engine.py L371-378: url + title share one try; any failure skips
	// the item.
	urlVal, ok := firstMatch(item, e.urlTokens)
	if !ok {
		return nil
	}
	titleVal, ok := firstMatch(item, e.titleTokens)
	if !ok {
		return nil
	}

	m := &result.MainResult{
		URL:   e.urlPrefix + toString(urlVal),
		Title: toString(titleVal),
	}
	if e.titleHTMLToText {
		m.Title = htmlToText(m.Title)
	}

	// json_engine.py L380-384: content is best-effort, defaulting to "".
	if len(e.contentTokens) > 0 {
		if contentVal, ok := firstMatch(item, e.contentTokens); ok {
			m.Content = toString(contentVal)
			if e.contentHTMLToText {
				m.Content = htmlToText(m.Content)
			}
		}
	}

	// json_engine.py L386-391: thumbnail only when configured; best-effort.
	if len(e.thumbnailTokens) > 0 {
		if thumbVal, ok := firstMatch(item, e.thumbnailTokens); ok {
			m.Thumbnail = e.thumbnailPrefix + toString(thumbVal)
		}
	}
	return m
}

// firstMatch is the Go counterpart of json_engine.py
// `query(result, q_query)[0]`: it returns the first match, unwrapping an
// array result (Python's query() always returns a list and the engine takes
// its [0] element).
func firstMatch(item any, tokens []string) (any, bool) {
	v, ok := Query(item, tokens)
	if !ok {
		return nil, false
	}
	if arr, isArr := v.([]any); isArr {
		if len(arr) == 0 {
			return nil, false
		}
		return arr[0], true
	}
	return v, true
}

// toString normalizes a decoded JSON value to string — port of SearXNG
// utils.py to_string(). json.Decoder.UseNumber preserves the int/float
// distinction, so json.Number.String() keeps the source literal.
//
// Deviations (documented): bool renders as Go "true"/"false" (Python
// "True"/"False"); nil renders as "" (Python "None"); float exponents keep
// their literal form ("1e+03" vs Python "1000.0").
func toString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		return strconv.FormatBool(t)
	case nil:
		return ""
	default:
		return fmt.Sprint(t)
	}
}

// decodeJSON decodes a response body into an any tree. json.Decoder with
// UseNumber preserves the int/float distinction; trailing tokens after the
// top-level value reject sloppy inputs such as "1 2" or "{} garbage".
func decodeJSON(body []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var data any
	if err := dec.Decode(&data); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("unexpected data after top-level JSON value")
	}
	return data, nil
}

// htmlToText strips HTML markup and collapses whitespace — a port of SearXNG
// utils.py html_to_text / HTMLTextExtractor using the golang.org/x/net/html
// tokenizer (already a direct dependency of nanuq-engine; TASK-010 allowed
// either htmlquery or a simple tag-stripping implementation — decision
// documented in the TASK-010 report).
//
// Behaviour mirrors the Python extractor: a tag stack (script/style contents
// are dropped), <br> becomes a space, unmatched end tags are kept as literal
// "</tag>" text, and whitespace is collapsed before parsing and trimmed at
// the end. Entities are decoded via the tokenizer (z.Text), which satisfies
// the Python docstring expectations ('&#x3e' -> '>') and is a documented
// improvement over HTMLParser's raw-name quirk for named entities.
//
// When the input leaves the tag stack unbalanced (the Python get_text
// assertion), the input is re-parsed escaped (html.EscapeString) so no markup
// survives — mirroring the Python html_to_text retry.
func htmlToText(s string) string {
	if s == "" {
		return ""
	}
	// utils.py: html_str.replace('\n', ' ').replace('\r', ' ') then
	// ' '.join(html_str.split()).
	s = strings.Join(strings.Fields(s), " ")

	text, balanced := extractHTMLText(s)
	if !balanced {
		// utils.py: except AssertionError -> feed(html.escape(html_str, True)).
		text, _ = extractHTMLText(html.EscapeString(s))
	}
	return strings.TrimSpace(text)
}

// extractHTMLText feeds s through the x/net/html tokenizer, emulating
// HTMLTextExtractor. balanced reports whether the tag stack drained
// completely (the Python get_text assertion).
func extractHTMLText(s string) (string, bool) {
	z := html.NewTokenizer(strings.NewReader(s))
	var stack []string
	var sb strings.Builder
	for {
		switch z.Next() {
		case html.ErrorToken:
			return sb.String(), len(stack) == 0
		case html.StartTagToken:
			name, _ := z.TagName()
			tag := string(name)
			if isVoidElement(tag) {
				// HTML5 void elements (br, img, ...) never push a stack
				// frame; without this a <br> would unbalance the stack and
				// the text would fall through to the escape retry.
				if tag == "br" {
					sb.WriteByte(' ')
				}
				continue
			}
			stack = append(stack, tag)
		case html.SelfClosingTagToken:
			name, _ := z.TagName()
			if string(name) == "br" {
				sb.WriteByte(' ')
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			tag := string(name)
			if len(stack) == 0 {
				continue // utils.py handle_endtag: `if not self.tags: return`
			}
			// utils.py handle_endtag: pops unconditionally and keeps a literal
			// "</tag>" when the popped tag does not match.
			popped := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if tag != popped {
				sb.WriteString("</" + tag + ">")
			}
		case html.TextToken:
			// utils.py handle_data + is_valid_tag: drop content inside
			// script/style.
			if !stackBlocked(stack) {
				sb.Write(z.Text())
			}
		}
		// CommentToken / DoctypeToken: ignored (utils.py never handles them).
	}
}

// stackBlocked reports whether the innermost open tag is script or style
// (utils.py _BLOCKED_TAGS, L43).
func stackBlocked(stack []string) bool {
	if len(stack) == 0 {
		return false
	}
	switch stack[len(stack)-1] {
	case "script", "style":
		return true
	}
	return false
}

// isVoidElement reports whether tag is an HTML5 void element (one that never
// has a matching end tag in well-formed HTML).
func isVoidElement(tag string) bool {
	switch tag {
	case "area", "base", "br", "col", "embed", "hr", "img", "input",
		"link", "meta", "param", "source", "track", "wbr":
		return true
	}
	return false
}

// --- Overrides accessors (yaml.v3 decoded types: string, bool, int,
// float64, map[string]any) ---

// strValue reads a string from Overrides.
func strValue(ov map[string]any, key string) (string, bool) {
	raw, ok := ov[key]
	if !ok {
		return "", false
	}
	s, ok := raw.(string)
	return s, ok
}

func strValueOr(ov map[string]any, key, def string) string {
	if s, ok := strValue(ov, key); ok {
		return s
	}
	return def
}

func intValue(ov map[string]any, key string, def int) int {
	raw, ok := ov[key]
	if !ok {
		return def
	}
	switch v := raw.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return def
}

func boolValue(ov map[string]any, key string, def bool) bool {
	raw, ok := ov[key]
	if !ok {
		return def
	}
	if b, ok := raw.(bool); ok {
		return b
	}
	return def
}

// strMap reads a string map (headers / cookies) from Overrides; yaml.v3
// decodes nested mappings as map[string]any.
func strMap(ov map[string]any, key string) map[string]string {
	raw, ok := ov[key]
	if !ok {
		return nil
	}
	out := make(map[string]string)
	switch m := raw.(type) {
	case map[string]any:
		for k, v := range m {
			out[k] = toString(v)
		}
	case map[string]string:
		return m
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
