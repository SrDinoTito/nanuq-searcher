// This file is a faithful Go port of SearXNG's searx/engines/mojeek.py.
// Documented deviations vs. the Python module are annotated inline and
// summarised in the RESULT report of TASK-010.
package engines

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/antchfx/htmlquery"
	"golang.org/x/net/html"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
	"nanuq-engine/internal/result"
)

// mojeekEngine implements engine.Engine for Mojeek results (mojeek.py).
type mojeekEngine struct {
	cfg *config.EngineConfig

	// baseURL ports the base_url module attribute (mojeek.py L21). SearXNG
	// settings may override module attributes, so it is read from
	// cfg.Overrides["base_url"] with the Python default as fallback.
	baseURL string

	// defCats are the module's categories (mojeek.py L24); they are used when
	// the YAML entry does not declare categories of its own.
	defCats []string
}

// NewMojeekEngine builds one Mojeek engine per YAML entry.
func NewMojeekEngine(cfg *config.EngineConfig) (engine.Engine, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%w: mojeek engine: nil config", engine.ErrInvalidConfig)
	}
	return &mojeekEngine{
		cfg:     cfg,
		baseURL: overrideStringDef(cfg.Overrides, "base_url", "https://www.mojeek.com"),
		defCats: []string{"general", "web"},
	}, nil
}

// Name returns the engine's configured name (YAML entry name).
func (e *mojeekEngine) Name() string { return e.cfg.Name }

// Shortcut returns the configured shortcut.
func (e *mojeekEngine) Shortcut() string { return e.cfg.Shortcut }

// Categories returns the configured categories, falling back to the module's
// default categories when the YAML entry declares none.
func (e *mojeekEngine) Categories() []string {
	if len(e.cfg.Categories) > 0 {
		return e.cfg.Categories
	}
	return e.defCats
}

// NeedsInit reports that no per-engine init is required.
func (e *mojeekEngine) NeedsInit() bool { return false }

// Setup is a no-op for the Mojeek engine (all config comes from Overrides).
func (e *mojeekEngine) Setup(_ context.Context, _ *config.EngineConfig) error { return nil }

// Init is a no-op. mojeek.py's init() validates search_type; in this port the
// module attribute is fixed at construction (web search).
func (e *mojeekEngine) Init(_ context.Context) error { return nil }

// Request mutates params to build the Mojeek search request. Port of
// mojeek.py request() (L96-108).
func (e *mojeekEngine) Request(query string, params *engine.RequestParams) error {
	if params == nil {
		return errors.New("mojeek engine: nil request params")
	}

	// mojeek.py L96-98: args = {"q": query, "safe": min(params["safesearch"],
	// 1)}.
	v := url.Values{}
	v.Set("q", query)
	safe := params.SafeSearch
	if safe > 1 {
		safe = 1
	}
	v.Set("safe", strconv.Itoa(safe))

	// mojeek.py L99-102: if pageno > 1: args["s"] = 10 * (pageno - 1). Page 1
	// must NOT send s=0 (it triggers a rate limit), so the offset is only set
	// for later pages — the task text's "s=pageno*10-10" is the same offset,
	// gated by the same pageno > 1 condition per the .py.
	if params.Pageno > 1 {
		v.Set("s", strconv.Itoa(10*(params.Pageno-1)))
	}

	// mojeek.py L103-105: params["url"] = base_url + "/search?" +
	// urlencode(args); the .py also sets cookies {lb: language, arc: region}
	// from the traits table, which is not ported — the search processor
	// supplies the locale via params.Language (see bingRegion for the same
	// convention).
	params.URL = e.baseURL + "/search?" + v.Encode()
	params.Method = http.MethodGet
	return nil
}

// Response parses the Mojeek HTML and extracts the organic results plus any
// spelling suggestions. Port of mojeek.py response() (L108-118) +
// _general_results (L126-141).
func (e *mojeekEngine) Response(resp *http.Response) ([]*result.RawResult, error) {
	if resp == nil {
		return nil, errors.New("mojeek engine: nil http response")
	}

	// mojeek.py L109: dom = html.fromstring(resp.text).
	doc, err := htmlquery.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("mojeek engine %q: parse response body: %w", e.cfg.Name, err)
	}

	results := []*result.RawResult{}

	// mojeek.py L126-127: results_xpath =
	// '//ul[@class="results-standard"]/li/a[@class="ob"]'. The task text's
	// looser selector (//ul[contains(@class,"results-standard")]/li with
	// h2/a/p) is honoured by using contains() on the ul class.
	items, err := htmlquery.QueryAll(doc, `//ul[contains(@class, "results-standard")]/li/a[@class="ob"]`)
	if err != nil {
		return nil, fmt.Errorf("mojeek engine %q: evaluate result xpath: %w", e.cfg.Name, err)
	}
	for _, item := range items {
		// mojeek.py L128-132: url_xpath = "./@href" (extract_text on the
		// attribute node, see extractText), title_xpath = "../h2/a",
		// content_xpath = '..//p[@class="s"]'.
		href := htmlquery.SelectAttr(item, "href")
		if href == "" {
			continue
		}
		title := extractTextFromNode(item, `../h2/a`)
		if title == "" {
			continue
		}
		content := extractTextFromNode(item, `..//p[@class="s"]`)

		results = append(results, result.NewMain(&result.MainResult{
			URL:     href,
			Title:   title,
			Content: content,
		}))
	}

	// mojeek.py L133-139: suggestions via suggestion_xpath =
	// '//div[@class="top-info"]/p[@class="top-info spell"]/em/a'.
	suggestions, err := htmlquery.QueryAll(doc, `//div[@class="top-info"]/p[@class="top-info spell"]/em/a`)
	if err == nil {
		for _, s := range suggestions {
			if text := extractText([]*html.Node{s}); text != "" {
				results = append(results, result.NewSuggestion(text))
			}
		}
	}

	slog.Debug("mojeek engine: extracted results", "engine", e.cfg.Name, "count", len(results))
	return results, nil
}
