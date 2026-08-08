// baidu.go is a faithful Go port of SearXNG's
// example/searxng/searx/engines/baidu.py with baidu_category = 'general'
// (baidu.py L37): the engine hits the JSON endpoint
// https://www.baidu.com/s?tn=json and parses data["feed"]["entry"].
//
// Scope (documented deviation): the 'images' and 'it' categories of
// baidu.py (L86-104) — including the image-cookie warmup cache (L42-65) and
// parse_images/parse_it — are NOT implemented; a "category" override other
// than "general" is rejected at construction. The publishedDate of
// parse_general (baidu.py L165-172) has no Go result model and is dropped.
//
// NOTE (deviation from the task text): the task described an HTML parse with
// htmlquery and selectors like div.result / h3.t a. The reference file
// parses JSON (tn=json), so this port does not use htmlquery at all (task
// rule: "port fiel", no invention).
package engines

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
	"nanuq-engine/internal/result"
)

// Sentinel errors porting the SearXNG engine exceptions raised by baidu.py:
// the wappass captcha redirect (L130-131, SearxEngineCaptchaException) and
// the antiFlag access denial (L138-139, SearxEngineAccessDeniedException).
var (
	errBaiduCaptcha      = errors.New("baidu: captcha detected")
	errBaiduAccessDenied = errors.New("baidu: access denied")
)

// baiduEngine implements engine.Engine for the Baidu general category.
type baiduEngine struct {
	cfg *config.EngineConfig

	resultsPerPage int              // results_per_page, baidu.py L35 (default 10)
	timeRangeMap   map[string]int64 // time_range_dict, baidu.py L40
}

// NewBaiduEngine builds one Baidu engine instance per YAML entry. Only the
// "general" category is implemented (baidu.py init(), L68-71, would reject
// anything else with an API exception).
func NewBaiduEngine(cfg *config.EngineConfig) (engine.Engine, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%w: baidu engine: nil config", engine.ErrInvalidConfig)
	}
	if cat, ok := overrideString(cfg.Overrides, "category"); ok && cat != "general" {
		return nil, fmt.Errorf("%w: baidu engine %q: unsupported category %q (only \"general\" is implemented)",
			engine.ErrInvalidConfig, cfg.Name, cat)
	}
	return &baiduEngine{
		cfg:            cfg,
		resultsPerPage: overrideIntDef(cfg.Overrides, "results_per_page", 10),
		timeRangeMap: map[string]int64{ // baidu.py L40
			"day":   86400,
			"week":  604800,
			"month": 2592000,
			"year":  31536000,
		},
	}, nil
}

// Name returns the engine's configured name (YAML entry name).
func (e *baiduEngine) Name() string { return e.cfg.Name }

// Shortcut returns the configured shortcut.
func (e *baiduEngine) Shortcut() string { return e.cfg.Shortcut }

// Categories returns the configured categories.
func (e *baiduEngine) Categories() []string { return e.cfg.Categories }

// NeedsInit reports that no per-engine init is required.
func (e *baiduEngine) NeedsInit() bool { return false }

// Setup is a no-op for the baidu engine (the Python setup() only creates the
// cookie cache, which belongs to the images category — out of scope).
func (e *baiduEngine) Setup(_ context.Context, _ *config.EngineConfig) error { return nil }

// Init is a no-op for the baidu engine (Python init() only validates the
// category, which the constructor already did).
func (e *baiduEngine) Init(_ context.Context) error { return nil }

// Request mutates params to build the Baidu general search request — port of
// baidu.py request() (L73-125) restricted to the 'general' branch. It
// performs no I/O.
func (e *baiduEngine) Request(query string, params *engine.RequestParams) error {
	if params == nil {
		return errors.New("baidu engine: nil request params")
	}
	if params.Pageno < 1 {
		params.Pageno = 1 // baidu.py L74: pageno drives pn; 0 would mean page 0
	}
	pageNum := params.Pageno

	// baidu.py L77-84 + L123: the general endpoint query string, built in
	// the same order as the Python dict {wd, rn, pn, tn} so the urlencode
	// output is stable. url.QueryEscape matches Python quote_plus (space
	// -> '+').
	queryParams := []string{
		"wd=" + url.QueryEscape(query),
		"rn=" + strconv.Itoa(e.resultsPerPage),
		"pn=" + strconv.Itoa((pageNum-1)*e.resultsPerPage),
		"tn=json",
	}

	// baidu.py L110-118: time_range support for the general category adds
	// gpc="stf={past},{now}|stftype=1".
	if seconds, ok := e.timeRangeMap[params.TimeRange]; ok {
		now := time.Now().Unix()
		past := now - seconds
		queryParams = append(queryParams, fmt.Sprintf("gpc=stf=%d,%d|stftype=1", past, now))
	}

	// baidu.py L123: params["url"] = f"{query_url}?{urlencode(query_params)}".
	params.URL = "https://www.baidu.com/s?" + strings.Join(queryParams, "&")
	params.Method = "GET"

	// baidu.py L124: params["allow_redirects"] = False — RequestParams has
	// no redirect field (network layer, TASK-007); the Response step still
	// inspects the Location header for the wappass captcha redirect, which
	// is what the flag exists for. Baidu's engine-level User-Agent handling
	// is delegated to the network layer, matching the Python (the engine
	// never sets headers itself, baidu.py L73-125).
	return nil
}

// Response converts an already-downloaded HTTP response into raw results —
// port of baidu.py response() (L128-142) + parse_general() (L145-173).
func (e *baiduEngine) Response(resp *http.Response) ([]*result.RawResult, error) {
	if resp == nil {
		return nil, errors.New("baidu engine: nil http response")
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("baidu engine %q: read response body: %w", e.cfg.Name, err)
	}

	// baidu.py L129-131: Baidu answers a redirect whose Location points at
	// the wappass captcha page; the Python raises SearxEngineCaptchaException.
	if strings.Contains(resp.Header.Get("Location"), "wappass.baidu.com/static/captcha") {
		return nil, fmt.Errorf("baidu engine %q: %w", e.cfg.Name, errBaiduCaptcha)
	}

	// baidu.py L133-137: json.loads(text, strict=False) — lenient about raw
	// control characters inside string literals; escapeControlChars emulates
	// that mode because Go's encoding/json is strict.
	data, err := decodeJSON(escapeControlChars(body))
	if err != nil {
		return nil, fmt.Errorf("baidu engine %q: invalid JSON response: %w", e.cfg.Name, err)
	}
	apiMap, ok := data.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("baidu engine %q: JSON response is not an object", e.cfg.Name)
	}

	// baidu.py L138-139: antiFlag == 1 → SearxEngineAccessDeniedException
	// with the server message (default "Forbid spider access").
	switch v := apiMap["antiFlag"].(type) {
	case json.Number:
		if v.String() == "1" {
			return nil, baiduAccessDenied(e.cfg.Name, apiMap)
		}
	case float64:
		if v == 1 {
			return nil, baiduAccessDenied(e.cfg.Name, apiMap)
		}
	}

	// baidu.py L140-142: dispatch by category — only 'general' is ported.
	return e.parseGeneral(apiMap)
}

// baiduAccessDenied builds the access-denied error (baidu.py L139).
func baiduAccessDenied(name string, data map[string]any) error {
	msg := toString(data["message"])
	if msg == "" {
		msg = "Forbid spider access"
	}
	return fmt.Errorf("baidu engine %q: %w: %s", name, errBaiduAccessDenied, msg)
}

// parseGeneral ports baidu.py parse_general() (L145-173).
func (e *baiduEngine) parseGeneral(data map[string]any) ([]*result.RawResult, error) {
	// baidu.py L147-148: `if not data.get("feed", {}).get("entry")` →
	// SearxEngineAPIException("Invalid response").
	feed, ok := data["feed"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("baidu engine %q: invalid response: missing feed", e.cfg.Name)
	}
	entryVal, ok := feed["entry"]
	if !ok {
		return nil, fmt.Errorf("baidu engine %q: invalid response: missing feed.entry", e.cfg.Name)
	}

	var entries []any
	switch ev := entryVal.(type) {
	case []any:
		entries = ev
	case map[string]any:
		entries = []any{ev}
	default:
		return nil, fmt.Errorf("baidu engine %q: invalid response: feed.entry has unexpected type %T", e.cfg.Name, entryVal)
	}

	results := make([]*result.RawResult, 0, len(entries))
	for _, ev := range entries {
		entry, ok := ev.(map[string]any)
		if !ok {
			continue
		}
		// baidu.py L151-152: an entry without a title or a url is skipped.
		title := toString(entry["title"])
		entryURL := toString(entry["url"])
		if title == "" || entryURL == "" {
			continue
		}

		// baidu.py L161-163: title and abs contain HTML entities (&amp;,
		// &#39;, &quot;, ...) that are unescaped (html.unescape port).
		title = html.UnescapeString(title)
		content := html.UnescapeString(toString(entry["abs"]))

		// baidu.py L153-160 (publishedDate from entry["time"]) and L165-172:
		// the Go MainResult model has no published-date field — dropped
		// (documented deviation).
		results = append(results, result.NewMain(&result.MainResult{
			Title:   title,
			URL:     entryURL,
			Content: content,
		}))
	}
	return results, nil
}

// escapeControlChars emulates Python's json.loads(strict=False) (baidu.py
// L137): raw control characters (U+0000-U+001F) inside string literals are
// accepted by the lenient mode but rejected by Go's encoding/json. Each such
// character is re-encoded as its JSON escape sequence: \t, \r and \n keep
// their two-letter forms, the rest become \u00XX. Characters outside string
// literals are left untouched.
func escapeControlChars(body []byte) []byte {
	var sb bytes.Buffer
	sb.Grow(len(body) + 16)
	inString := false
	escaped := false
	for _, b := range body {
		switch {
		case escaped:
			sb.WriteByte(b)
			escaped = false
		case inString && b == '\\':
			sb.WriteByte(b)
			escaped = true
		case b == '"':
			sb.WriteByte(b)
			inString = !inString
		case inString && b < 0x20:
			switch b {
			case '\t':
				sb.WriteString(`\t`)
			case '\r':
				sb.WriteString(`\r`)
			case '\n':
				sb.WriteString(`\n`)
			default:
				fmt.Fprintf(&sb, `\u%04x`, b)
			}
		default:
			sb.WriteByte(b)
		}
	}
	return sb.Bytes()
}
