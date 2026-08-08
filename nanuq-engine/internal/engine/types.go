// Package engine defines the engine contracts of nanuq-server.
//
// DSG-002 / REQ-005: an Engine models the full search lifecycle — conditional
// async init, a pure Request step that mutates RequestParams without I/O, and
// a Response step that consumes an already-downloaded HTTP response. The
// processor (TASK-006) orchestrates timeout, suspend and extend around these
// methods.
//
// This package is contract-only (DECISION-006): it MUST NOT import
// internal/engines. Concrete implementations live in internal/engines and are
// registered from cmd/nanuq-server/main.go (TASK-011), which breaks the
// circular risk RISK-001.
package engine

import (
	"context"
	"net/http"
	"time"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/result"
)

// Engine is the contract every search engine module implements (DSG-002,
// REQ-005). The lifecycle is:
//
//  1. Setup  — synchronous, once, at startup (default: no-op).
//  2. Init   — asynchronous, run in a goroutine only when NeedsInit is true
//     (e.g. a thread-daemon that keeps a session warm).
//  3. Request — pure: mutates params (URL, Method, Headers, ...) without any
//     I/O. The network layer (TASK-007) executes the resulting params.
//  4. Response — receives the already-downloaded *http.Response and converts
//     it into raw results (or an error).
type Engine interface {
	// Name returns the instance name (the YAML entry name, e.g.
	// "duckduckgo_extra").
	Name() string

	// Shortcut returns the instance shortcut, or "" when none is set.
	Shortcut() string

	// Categories returns the categories this instance serves (general,
	// images, videos, news, ...).
	Categories() []string

	// NeedsInit reports whether the engine requires the asynchronous Init
	// step. It must be safe to call before Init.
	NeedsInit() bool

	// Setup performs synchronous one-time initialization (default: nil).
	Setup(ctx context.Context, cfg *config.EngineConfig) error

	// Init performs asynchronous initialization and runs until the context is
	// cancelled (default: nil). It is invoked in a goroutine only when
	// NeedsInit reports true.
	Init(ctx context.Context) error

	// Request builds the outgoing request into params — it MUTATES params and
	// performs no I/O. query is the raw search query.
	Request(query string, params *RequestParams) error

	// Response converts an already-downloaded HTTP response into raw results.
	Response(resp *http.Response) ([]*result.RawResult, error)
}

// RequestParams carries everything needed to execute one engine request
// (DSG-002, section 4.1). Engines write Method and URL (and optionally
// Headers, Data, JSON, Cookies); the network layer (TASK-007) executes them.
type RequestParams struct {
	// Method is the HTTP method, default "GET".
	Method string

	// URL is the fully-built request URL, mutated by Engine.Request.
	URL string

	// Headers are the request headers, written by Engine.Request.
	Headers http.Header

	// Data are form-encoded body values, used when Method is POST-like.
	Data map[string]string

	// JSON is an optional JSON body payload, used when Method is POST-like.
	JSON any

	// Cookies are the request cookies.
	Cookies []*http.Cookie

	// Timeout overrides the per-request timeout.
	Timeout time.Duration

	// TimeRange restricts the search to a time window (engine-specific
	// syntax, e.g. "day", "week", "month").
	TimeRange string

	// Pageno is the 1-based result page requested.
	Pageno int

	// SafeSearch is the safe-search level (0 = off, higher = stricter,
	// engine-specific).
	SafeSearch int

	// Language is the ISO language code, e.g. "en", "es".
	Language string
}
