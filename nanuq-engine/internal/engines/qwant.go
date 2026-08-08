// This file is a faithful Go port of SearXNG's searx/engines/qwant.py (web
// results). Documented deviations vs. the Python module are annotated inline
// and summarised in the RESULT report of TASK-010.
package engines

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
	"nanuq-engine/internal/result"
)

// qwantEngine implements engine.Engine for Qwant web results (qwant.py).
type qwantEngine struct {
	cfg *config.EngineConfig
}

// NewQwantEngine builds one Qwant (web) engine per YAML entry.
//
// DEVIATION vs. qwant.py: the Python module (as installed in
// example/searxng) issues a GET against https://api.qwant.com/v3/search/web
// with urlencoded args (q, count, locale, offset, tgp, device, safesearch,
// displayed, llm) plus a datadome cookie (qwant.py L135-151). The nanuq-engine
// spec fixes the request contract as a POST to
// https://api.qwant.com/api/search/web with a JSON body {q, locale, offset,
// safesearch} and Content-Type application/json — which is what this port
// implements. Response parsing below follows the .py exactly.
func NewQwantEngine(cfg *config.EngineConfig) (engine.Engine, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%w: qwant engine: nil config", engine.ErrInvalidConfig)
	}
	return &qwantEngine{cfg: cfg}, nil
}

// Name returns the engine's configured name (YAML entry name).
func (e *qwantEngine) Name() string { return e.cfg.Name }

// Shortcut returns the configured shortcut.
func (e *qwantEngine) Shortcut() string { return e.cfg.Shortcut }

// Categories returns the configured categories. qwant.py declares an empty
// categories list, so the YAML entry's categories are returned as-is.
func (e *qwantEngine) Categories() []string { return e.cfg.Categories }

// NeedsInit reports that no per-engine init is required.
func (e *qwantEngine) NeedsInit() bool { return false }

// Setup is a no-op for the Qwant engine (all config comes from Overrides).
func (e *qwantEngine) Setup(_ context.Context, _ *config.EngineConfig) error { return nil }

// Init is a no-op. qwant.py's init() caches the datadome cookie from
// settings; in this port cookies are supplied by the search processor.
func (e *qwantEngine) Init(_ context.Context) error { return nil }

// qwantLocale resolves the locale for a request. Port of qwant.py's
// traits.get_region default "en_US": the traits table is not ported, so the
// search processor supplies the locale via params.Language, defaulting to the
// Python default.
func qwantLocale(params *engine.RequestParams) string {
	if params.Language != "" {
		return params.Language
	}
	return "en_US"
}

// Request mutates params to build the Qwant API request.
//
// DEVIATION vs. qwant.py: the .py builds a GET with urlencoded args
// (qwant.py L135-151). The nanuq-engine spec fixes a POST with a JSON body
// {q, locale, offset, safesearch} and Content-Type application/json —
// implemented here per spec. The offset follows the .py pagination:
// (pageno - 1) * 10 (qwant.py L143), and safesearch is passed through
// verbatim like the .py args dict.
func (e *qwantEngine) Request(query string, params *engine.RequestParams) error {
	if params == nil {
		return errors.New("qwant engine: nil request params")
	}

	// qwant.py L143: "offset": (params["pageno"] - 1) * 10.
	pageno := params.Pageno
	if pageno < 1 {
		pageno = 1
	}

	params.JSON = map[string]any{
		"q":          query,
		"locale":     qwantLocale(params),
		"offset":     (pageno - 1) * 10,
		"safesearch": params.SafeSearch,
	}
	params.URL = "https://api.qwant.com/api/search/web"
	params.Method = http.MethodPost
	if params.Headers == nil {
		params.Headers = make(http.Header)
	}
	params.Headers.Set("Content-Type", "application/json")
	return nil
}

// Response parses the Qwant API JSON and extracts the organic web results.
// Port of qwant.py response() (L152-234) restricted to the "web" category.
//
// DEVIATION vs. qwant.py: the .py raises typed exceptions on a non-"success"
// status (TooManyRequests / Captcha / AccessDenied / APIException, qwant.py
// L156-162); this Go port has no such exception surface, so a non-"success"
// status yields an empty result set (defensive, EC-011). Invalid items are
// skipped per spec.
func (e *qwantEngine) Response(resp *http.Response) ([]*result.RawResult, error) {
	if resp == nil {
		return nil, errors.New("qwant engine: nil http response")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("qwant engine %q: read response body: %w", e.cfg.Name, err)
	}

	searchResults, err := decodeJSON(body)
	if err != nil {
		return nil, fmt.Errorf("qwant engine %q: decode response body: %w", e.cfg.Name, err)
	}

	results := []*result.RawResult{}

	// qwant.py L156-162: if search_results.get("status") != "success" the
	// Python raises; here a non-success status simply yields no results.
	if status, ok := Query(searchResults, Parse("status")); ok && toString(status) != "success" {
		slog.Debug("qwant engine: non-success status", "engine", e.cfg.Name, "status", toString(status))
		return results, nil
	}

	// qwant.py L165-168: mainline = data.get("result", {}).get("items",
	// {}).get("mainline", {}). The Python default {} is a dict but the API
	// returns a list in practice; the task text specifies
	// data.result.items.mainline.
	mainline, found := Query(searchResults, Parse("data/result/items/mainline"))
	if !found {
		slog.Debug("qwant engine: no mainline in response", "engine", e.cfg.Name)
		return results, nil
	}

	rows, ok := mainline.([]any)
	if !ok {
		slog.Debug("qwant engine: mainline is not a list", "engine", e.cfg.Name)
		return results, nil
	}

	for _, row := range rows {
		rowMap, ok := row.(map[string]any)
		if !ok {
			continue
		}

		// qwant.py L171-172: mainline_type = row.get("type", "web"); rows
		// whose type is not the search category are skipped (this also drops
		// the "ads" rows, qwant.py L174).
		mainlineType := "web"
		if t, has := rowMap["type"]; has {
			if s, ok := t.(string); ok && s != "" {
				mainlineType = s
			}
		}
		if mainlineType != "web" {
			continue
		}

		items, ok := rowMap["items"].([]any)
		if !ok {
			continue
		}
		for _, item := range items {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}

			// Task text: items with _type "web" are the web results; the .py
			// filters on the row type (done above). Both are honoured: an
			// item-level _type that is set but not "web" is skipped.
			if t, has := itemMap["_type"]; has {
				if s, ok := t.(string); ok && s != "" && s != "web" {
					continue
				}
			}

			// qwant.py L176-181 (web branch): title, url, content from the
			// item fields; invalid items (missing/empty url) are skipped.
			url := toString(itemMap["url"])
			if url == "" {
				continue
			}
			title := toString(itemMap["title"])
			content := toString(itemMap["desc"])

			results = append(results, result.NewMain(&result.MainResult{
				URL:     url,
				Title:   title,
				Content: content,
			}))
		}
	}

	slog.Debug("qwant engine: extracted results", "engine", e.cfg.Name, "count", len(results))
	return results, nil
}
