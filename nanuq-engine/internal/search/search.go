package search

// This file implements the SearchService — a faithful Go port of SearXNG's
// Search class (searx/search/__init__.py L53-179, REQ-003). Search
// orchestrates the full pipeline: external bang redirect (L59-69),
// answerers (L71-75) and the standard multi-engine search (L78-171).
//
// The standard search follows the port's structure:
//   - searchStandard  -> _get_requests + search_multiple_requests
//     (__init__.py L160-171)
//   - getRequests     -> _get_requests (__init__.py L78-134): builds one
//     request per engine, skipping suspended / non-requestable engines,
//     and derives the global timeout (4 branches).
//   - searchMultipleRequests -> search_multiple_requests (__init__.py
//     L136-158): one goroutine per engine with a watchdog timeout, as
//     specified by DSG-005 (errgroup + per-engine context).
//
// Per-engine work (parameter preparation, request execution, error
// handling) is delegated to EngineProcessor (processor.go).

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"golang.org/x/sync/errgroup"

	"nanuq-engine/internal/bang"
	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
	"nanuq-engine/internal/result"
)

// BangResolver is the bang-store contract consumed by the search pipeline.
// It extends bang.BangStore (Lookup) with URL resolution (GetBangURL).
//
// The concrete *bang.Store satisfies this interface. The TASK-006 sketch
// typed the field as bang.BangStore, but that interface (TASK-005) exposes
// only Lookup while search_external_bang needs the resolved URL — so the
// search package declares this local extension instead of modifying
// internal/bang (out of scope).
type BangResolver interface {
	Lookup(name string) (bang.BangDef, bool)
	GetBangURL(name, query string) (string, bool)
}

// SearchService orchestrates a search across the configured engines
// (port of the Search class, searx/search/__init__.py L53+; REQ-003).
type SearchService struct {
	registry   *engine.Registry
	store      BangResolver
	catalog    EngineCatalog
	cfg        *config.Config
	processors map[string]*EngineProcessor
	requester  Requester
	answerers  *AnswererStorage
}

// New creates a SearchService and instantiates one EngineProcessor per
// enabled engine in cfg.Engines (port of the processor construction in
// searx/search/__init__.py L90-93 and searx/engines/__init__.py
// load_engines; REQ-003). Engines marked Disabled or Inactive are
// skipped; engines that fail to instantiate are logged and skipped (their
// processor simply never gets created — _get_requests skips unknown
// engine names).
func New(registry *engine.Registry, store BangResolver, catalog EngineCatalog, cfg *config.Config, requester Requester, answerers *AnswererStorage) *SearchService {
	s := &SearchService{
		registry:   registry,
		store:      store,
		catalog:    catalog,
		cfg:        cfg,
		processors: make(map[string]*EngineProcessor),
		requester:  requester,
		answerers:  answerers,
	}

	for i := range cfg.Engines {
		ecfg := cfg.Engines[i]
		if ecfg.Disabled || ecfg.Inactive {
			// Skipped engines never get a processor (port of the
			// engines.SKIPPED handling, searx/engines/__init__.py).
			continue
		}
		e, err := registry.Instantiate(ecfg)
		if err != nil {
			slog.Debug("search: engine instantiation failed", "engine", ecfg.Name, "error", err)
			continue
		}
		proc := NewProcessor(e, &ecfg, requester)
		// Apply the global suspension policy (config.Search holds
		// ban_time_on_fail / max_ban_time_on_fail / suspended_times).
		proc.SetSuspensionPolicy(cfg.Search.BanTimeOnFail, cfg.Search.MaxBanTimeOnFail, cfg.Search.SuspendedTimes)
		s.processors[ecfg.Name] = proc
	}

	return s
}

// Search runs the search pipeline for a parsed query and returns the
// result container (port of Search.search(), __init__.py L174-179;
// REQ-003). The pipeline is: external bang redirect → answerers →
// standard multi-engine search. The container is always returned (never
// nil); when no search branch applied it simply holds no results.
func (s *SearchService) Search(rawText *RawTextQuery) *ResultContainer {
	ctx := context.Background()
	sq := s.buildSearchQuery(rawText)
	container := NewResultContainer()

	if s.searchExternalBang(ctx, sq, container) {
		return container
	}
	if s.searchAnswerers(ctx, sq, container) {
		return container
	}
	s.searchStandard(ctx, sq, container)
	return container
}

// buildSearchQuery converts a parsed RawTextQuery into the internal
// SearchQuery used by the pipeline (port of Search.__init__,
// __init__.py L81-101). Defaults: Lang "all", Pageno 1, SafeSearch from
// config, TimeRange "" (no time range in TASK-006 part B). The engine
// list is either the user's explicit refs (specific query) or every
// engine in the "all" category of the catalog.
func (s *SearchService) buildSearchQuery(rawText *RawTextQuery) *SearchQuery {
	lang := "all"
	if len(rawText.Languages) > 0 {
		lang = rawText.Languages[0]
	}
	// TASK-006: page 1 unless the query explicitly requests a further
	// page. RawTextQuery carries no page information (the parser does not
	// extract paging tokens yet), so the default is always 1.
	sq := &SearchQuery{
		Query:                 rawText.GetQuery(),
		Lang:                  lang,
		SafeSearch:            s.cfg.Search.SafeSearch,
		Pageno:                1,
		TimeRange:             "",
		TimeoutLimit:          rawText.TimeoutLimit,
		ExternalBang:          rawText.ExternalBang,
		Specific:              rawText.Specific,
		RedirectToFirstResult: rawText.RedirectToFirstResult,
	}

	if rawText.Specific && len(rawText.Enginerefs) > 0 {
		sq.EngineRefs = append(sq.EngineRefs, rawText.Enginerefs...)
	} else {
		sq.EngineRefs = s.allEngineRefs()
	}
	return sq
}

// allEngineRefs returns one EngineRef per engine in the catalog's "all"
// category (the SearXNG convention: "all" lists every enabled engine).
// When the catalog has no "all" category the refs are empty and the
// standard search yields no requests.
func (s *SearchService) allEngineRefs() []EngineRef {
	names, ok := s.catalog.EnginesInCategory("all")
	if !ok {
		return nil
	}
	refs := make([]EngineRef, 0, len(names))
	for _, n := range names {
		refs = append(refs, EngineRef{Name: n, Category: "all"})
	}
	return refs
}

// searchExternalBang handles an external bang redirect (port of
// search_external_bang(), __init__.py L59-69; REQ-003, CA-008). When the
// parsed query carries an ExternalBang, the redirect URL is computed from
// the bang store and set on the container; the rest of the pipeline is
// skipped.
func (s *SearchService) searchExternalBang(ctx context.Context, sq *SearchQuery, container *ResultContainer) bool {
	if sq.ExternalBang == "" {
		return false
	}
	url, ok := s.store.GetBangURL(sq.ExternalBang, sq.Query)
	if !ok {
		// Unknown bang: no redirect, fall through to the next branch.
		return false
	}
	container.SetRedirectURL(url)
	return true
}

// searchAnswerers runs the registered answerers (port of
// search_answerers(), __init__.py L71-75; REQ-003). Their raw results are
// extended into the container without an engine name (the Python passes
// None as engine_name). When at least one answerer produced results, the
// rest of the pipeline is skipped.
func (s *SearchService) searchAnswerers(ctx context.Context, sq *SearchQuery, container *ResultContainer) bool {
	if s.answerers == nil {
		return false
	}
	results := s.answerers.Ask(ctx, sq.Query)
	if len(results) == 0 {
		return false
	}
	container.Extend("", results)
	return true
}

// searchStandard runs the standard multi-engine search (port of
// search_standard(), __init__.py L160-171): it collects the requests and
// dispatches them concurrently, unless no engine produced a request.
func (s *SearchService) searchStandard(ctx context.Context, sq *SearchQuery, container *ResultContainer) {
	requests, actualTimeout := s.getRequests(sq)
	if len(requests) == 0 {
		return
	}
	s.searchMultipleRequests(ctx, requests, actualTimeout, sq, container)
}

// searchRequest is one engine request to dispatch (port of the
// (engine_name, query, params) tuples of _get_requests, __init__.py
// L101-110).
type searchRequest struct {
	name    string
	params  *engine.RequestParams
	timeout time.Duration
}

// getRequests builds the per-engine requests and computes the global
// timeout (port of _get_requests(), __init__.py L78-134; REQ-003). An
// engine is skipped when it has no processor, is suspended (REQ-008) or
// its GetParams returns nil (unsupported paging / time range). The global
// timeout follows the four branches of __init__.py L111-126 (the Python
// max() over engine timeouts is not ported — TASK-006 specifies the
// max_request_timeout / timeout_limit interaction only):
//
//	MaxRequestTimeout>0 && TimeoutLimit!=nil -> min of both
//	MaxRequestTimeout>0                     -> MaxRequestTimeout
//	TimeoutLimit!=nil                       -> *TimeoutLimit
//	neither                                 -> 0 (no global timeout)
func (s *SearchService) getRequests(sq *SearchQuery) ([]searchRequest, float64) {
	var requests []searchRequest

	for _, ref := range sq.EngineRefs {
		proc := s.processors[ref.Name]
		if proc == nil || proc.IsSuspended() {
			// Unknown engine or currently suspended (REQ-008): skip.
			continue
		}
		params, err := proc.GetParams(sq, ref.Category)
		if err != nil || params == nil {
			// Unsupported paging / time range: skip this engine.
			continue
		}
		requests = append(requests, searchRequest{
			name:    ref.Name,
			params:  params,
			timeout: durationSeconds(proc.cfg.Timeout),
		})
	}

	actualTimeout := s.globalTimeout(sq)
	return requests, actualTimeout
}

// globalTimeout derives the global search timeout from the config and the
// query (port of the four branches of _get_requests, __init__.py L111-126).
// It returns 0 when no global timeout applies (no max_request_timeout and
// no query timeout limit).
func (s *SearchService) globalTimeout(sq *SearchQuery) float64 {
	max := s.cfg.Outgoing.MaxRequestTimeout
	limit := sq.TimeoutLimit

	switch {
	case max > 0 && limit != nil:
		return math.Min(max, *limit)
	case max > 0:
		return max
	case limit != nil:
		return *limit
	default:
		return 0
	}
}

// procResult is the outcome of one engine's search, delivered over the
// watchdog channel.
type procResult struct {
	results []*result.RawResult
	err     error
}

// searchMultipleRequests dispatches the engine requests concurrently and
// applies the watchdogs (port of search_multiple_requests(), __init__.py
// L136-158, as specified by DSG-005): an errgroup drives the engine
// goroutines, a per-engine context enforces the engine timeout, and a
// select waits for either the engine result or the engine context
// cancellation. When the engine context fires first, the engine is
// reported unresponsive with reason "timeout" (EC-003, CA-004).
//
// A global timeout (actualTimeout > 0) wraps the whole group with
// context.WithTimeout, mirroring the Python join(remaining_time) watchdog
// (__init__.py L148-157). Engine failures never abort the group — every
// engine fails individually and the container records it (the Python
// thread model has no group-level error either).
//
// Deviation from the TASK-006 sketch: the per-engine select uses the
// watchdog goroutine + channel of DSG-005 instead of the inline
// "select { <-ctx.Done(); default: }" — an inline default only observes a
// pre-existing cancellation and cannot mark a hung engine 'timeout' while
// its request is still running, which CA-004 requires.
func (s *SearchService) searchMultipleRequests(ctx context.Context, requests []searchRequest, actualTimeout float64, sq *SearchQuery, container *ResultContainer) {
	if actualTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, durationSeconds(actualTimeout))
		defer cancel()
	}

	g, ctx := errgroup.WithContext(ctx)

	for _, req := range requests {
		req := req // capture the loop variable (Go < 1.22 semantics)
		g.Go(func() error {
			// Per-engine watchdog: a context whose deadline is the engine
			// timeout, nested in the (possibly global) group context —
			// DSG-005 "goroutine per engine with nested timeout".
			ctxEngine := ctx
			var cancelEngine context.CancelFunc
			if req.timeout > 0 {
				ctxEngine, cancelEngine = context.WithTimeout(ctx, req.timeout)
				defer cancelEngine()
			}

			proc := s.processors[req.name]
			start := time.Now()
			ch := make(chan procResult, 1)
			go func() {
				results, err := proc.Search(ctxEngine, sq, req.params)
				ch <- procResult{results: results, err: err}
			}()

			select {
			case <-ctxEngine.Done():
				// Watchdog fired (engine timeout, or the global timeout
				// cancelled the group): report the engine as unresponsive
				// (port of th.join(remaining_time) + is_alive, __init__.py
				// L148-152; EC-003, CA-004).
				container.AddUnresponsiveEngine(req.name, "timeout")
				return nil
			case r := <-ch:
				// The engine finished before its watchdog: extend the
				// container and record the timing (port of
				// extend_container + add_timing, abstract.py L220-233 and
				// results.py L257-262).
				container.AddTiming(req.name, time.Since(start))
				if r.err != nil {
					// Suspendable errors (network / 429 / 403 class)
					// suspend the engine; other errors only report it
					// (port of handle_exception, abstract.py L175-201;
					// REQ-008). The processor marks suspendable errors
					// with errSuspendable (errors.As unwraps the chain).
					var suspendable *errSuspendable
					proc.HandleException(container, req.name, r.err, errors.As(r.err, &suspendable))
					return nil
				}
				container.Extend(req.name, r.results)
				return nil
			}
		})
	}

	// The group error is ignored by design: engines fail individually and
	// are recorded in the container (no engine failure aborts the search).
	_ = g.Wait()
}

// Processor returns the processor of the named engine, or nil when the
// engine is not configured / failed to instantiate. Exposed for tests and
// for engine-init tooling.
func (s *SearchService) Processor(name string) *EngineProcessor {
	return s.processors[name]
}

// String returns a debug description of the service (used in logs).
func (s *SearchService) String() string {
	return fmt.Sprintf("SearchService{engines=%d}", len(s.processors))
}
