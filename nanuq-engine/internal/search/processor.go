package search

// This file implements the EngineProcessor — a faithful Go port of
// SearXNG's EngineProcessor / OnlineProcessor classes (searx/search/
// processors/abstract.py L112-308, online.py L241-284). Each processor
// wraps one engine instance plus its suspension state and performs the
// per-engine part of a search: parameter preparation (get_params),
// request execution (Request → Requester.Do → Response), error handling
// with the suspend policy (handle_exception, REQ-008) and conditional
// init (NeedsInit/Setup/Init).

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
	"nanuq-engine/internal/result"
)

// Requester executes the actual HTTP request prepared by an engine. It is
// a seam for the network layer (TASK-007): the search pipeline only needs
// Do(ctx, params) -> Response. Implementations must honour ctx
// cancellation so a watchdog timeout (DSG-005, CA-004) can abort a hung
// engine.
type Requester interface {
	// Do performs the request described by params and returns the HTTP
	// response, or an error (network failure, timeout, HTTP-level error).
	// The returned error, when it wraps engine.EngineSuspendError (429 /
	// 403 class failures), marks the engine for suspension (REQ-008).
	Do(ctx context.Context, params *engine.RequestParams) (*http.Response, error)
}

// errSuspendable marks an error that must lead to the engine being
// suspended (REQ-008): request/response failures of the 429/403 class
// (Captcha, TooManyRequests, AccessDenied) and network/timeout errors.
// It wraps the underlying error so errors.As/Is still match, and the
// caller (search.go, searchMultipleRequests) decides what to do via
// errors.As.
type errSuspendable struct {
	err error
}

func (e *errSuspendable) Error() string { return e.err.Error() }
func (e *errSuspendable) Unwrap() error { return e.err }

// suspendable wraps err as an errSuspendable. It returns nil when err is
// nil so callers can pass the result straight into the pipeline.
func suspendable(err error) error {
	if err == nil {
		return nil
	}
	return &errSuspendable{err: err}
}

// isSuspendableError reports whether err (or anything it wraps with %w)
// is an engine.EngineSuspendError — the 429/403 class failure used by the
// engines (Captcha/TooManyRequests/AccessDenied, REQ-008, EC-005).
func isSuspendableError(err error) bool {
	var susErr *engine.EngineSuspendError
	return errors.As(err, &susErr)
}

// EngineProcessor couples one engine instance with its configuration and
// suspension state (port of EngineProcessor, abstract.py L112+; REQ-008).
type EngineProcessor struct {
	engine    engine.Engine
	cfg       *config.EngineConfig
	suspended *SuspendedStatus
	requester Requester
}

// NewProcessor creates an EngineProcessor for the given engine. The
// suspension status starts with a zero policy (0s bans): the ban policy
// lives on config.Search (ban_time_on_fail / max_ban_time_on_fail /
// suspended_times), which SearchService.New applies afterwards by
// replacing the status with NewSuspendedStatus. NewProcessor is also used
// directly by tests with their own policy.
func NewProcessor(e engine.Engine, cfg *config.EngineConfig, requester Requester) *EngineProcessor {
	return &EngineProcessor{
		engine:    e,
		cfg:       cfg,
		suspended: NewSuspendedStatus(0, 0, nil),
		requester: requester,
	}
}

// SetSuspensionPolicy replaces the processor's suspension policy. It is
// called by SearchService.New to apply the global search policy
// (config.Search.BanTimeOnFail / MaxBanTimeOnFail / SuspendedTimes) to
// every processor.
func (p *EngineProcessor) SetSuspensionPolicy(banTimeOnFail, maxBanTimeOnFail int, suspendedTimes map[string]int) {
	p.suspended = NewSuspendedStatus(banTimeOnFail, maxBanTimeOnFail, suspendedTimes)
}

// GetParams builds the engine.RequestParams for one engine given the
// search query and the engine's category (port of get_params(),
// abstract.py L243-289). A nil return (with nil error) means "skip this
// engine": the engine does not support the requested page (paging) or the
// search's time range — the caller (search.go _getRequests) skips it.
func (p *EngineProcessor) GetParams(sq *SearchQuery, category string) (*engine.RequestParams, error) {
	// paging: page 1 is always supported; later pages only when the
	// engine opts in (engine.paging, abstract.py L243-246). The Go Engine
	// interface (types.go) does not expose paging, so the flag is carried
	// by the engine config override "paging" (TASK-006).
	if sq.Pageno > 1 && !p.supportsPaging() {
		return nil, nil
	}

	params := &engine.RequestParams{
		Method:     "GET",
		URL:        "",
		Pageno:     sq.Pageno,
		SafeSearch: sq.SafeSearch,
		Language:   sq.Lang,
		TimeRange:  sq.TimeRange,
	}
	if p.cfg.Timeout > 0 {
		params.Timeout = durationSeconds(p.cfg.Timeout)
	}

	// time_range: engines that do not support it get an empty range
	// (abstract.py L249-250). Like paging, the capability flag lives in
	// the engine config override "time_range_support".
	if sq.TimeRange != "" && !p.supportsTimeRange() {
		params.TimeRange = ""
	}

	return params, nil
}

// supportsPaging reports whether the engine's config declares paging
// support via the "paging" override (there is no paging on the Engine
// interface, so the capability is config-driven — TASK-006).
func (p *EngineProcessor) supportsPaging() bool {
	v, ok := p.cfg.Overrides["paging"]
	return ok && v == true
}

// supportsTimeRange reports whether the engine's config declares time
// range support via the "time_range_support" override (same rationale as
// supportsPaging).
func (p *EngineProcessor) supportsTimeRange() bool {
	v, ok := p.cfg.Overrides["time_range_support"]
	return ok && v == true
}

// Search runs one engine against the query: Request (build URL/params),
// Requester.Do (perform HTTP), Response (parse results) — port of
// OnlineProcessor.search / _search_basic (online.py L225-284). Errors are
// returned to the caller (searchMultipleRequests) rather than handled
// here, because the TASK-006 signature has no container; the caller
// routes them through HandleException. Network / request errors and
// 429/403-class engine errors are wrapped as errSuspendable so the caller
// suspends the engine (REQ-008, EC-005).
func (p *EngineProcessor) Search(ctx context.Context, sq *SearchQuery, params *engine.RequestParams) ([]*result.RawResult, error) {
	// engine.request(query, params) — mutates params (URL, headers...).
	if err := p.engine.Request(sq.Query, params); err != nil {
		return nil, err
	}

	// network GET/POST via the requester seam (TASK-007 implements the
	// real transport; online.py _send_http_request L239-...).
	resp, err := p.requester.Do(ctx, params)
	if err != nil {
		// (httpx errors / ssl / timeouts, online.py L270-282): suspend.
		return nil, suspendable(err)
	}
	if resp == nil {
		return nil, suspendable(fmt.Errorf("engine: %s: empty response", p.engine.Name()))
	}

	// engine.response(response) — parse the payload into raw results.
	results, err := p.engine.Response(resp)
	if err != nil {
		if isSuspendableError(err) {
			// Captcha / TooManyRequests / AccessDenied (REQ-008, EC-005):
			// suspendable.
			return nil, suspendable(err)
		}
		return nil, err
	}
	return results, nil
}

// HandleException records a failed engine and applies the suspension
// policy when requested (port of handle_exception(), abstract.py L175-201;
// REQ-008). The engine is reported as unresponsive (EC-003); when suspend
// is set, the engine is suspended with the reason carried by a wrapped
// engine.EngineSuspendError, or with the generic "exception" reason.
func (p *EngineProcessor) HandleException(container *ResultContainer, name string, err error, suspend bool) {
	container.AddUnresponsiveEngine(name, err.Error())

	if !suspend {
		return
	}

	var susErr *engine.EngineSuspendError
	if errors.As(err, &susErr) {
		p.suspended.Suspend(susErr.Reason)
		return
	}
	p.suspended.Suspend("exception")
}

// InitEngine runs the optional engine initialisation (port of
// init_engine(), abstract.py L152-173): Setup then Init, only when the
// engine declares NeedsInit(). On failure the engine is suspended with
// the "init" reason (the Python original logs and returns False; here the
// suspension keeps the engine out of subsequent searches).
func (p *EngineProcessor) InitEngine(ctx context.Context) error {
	if !p.engine.NeedsInit() {
		return nil
	}
	if err := p.engine.Setup(ctx, p.cfg); err != nil {
		p.suspended.Suspend("init")
		return err
	}
	if err := p.engine.Init(ctx); err != nil {
		p.suspended.Suspend("init")
		return err
	}
	return nil
}

// IsSuspended reports whether the engine is currently suspended (port of
// extend_container_if_suspended, abstract.py L235-241; used by
// search.go _getRequests to skip suspended engines).
func (p *EngineProcessor) IsSuspended() bool {
	return p.suspended.IsSuspended()
}

// durationSeconds converts a duration expressed in seconds (float, the
// config unit) into a time.Duration.
func durationSeconds(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
}
