// Package nanuq is the public facade of nanuq-engine (TASK-022, DSG-018).
//
// It exposes a clean, HTTP-free API for MCP-style consumers over the
// internal packages: engine factory (Factory), engine metadata (Engine,
// EngineInfo) and the search pipeline (Service, Result). The internal
// engine interface is never exported — SearchEngine adapts an internal
// engine.Engine instance by delegation.
package nanuq

import (
	"context"
	"net/http"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
	"nanuq-engine/internal/result"
)

// Engine is the public, consumer-facing description of an engine instance.
//
// It is derived from a constructed internal engine and carries the
// instance metadata (Name, Shortcut, Categories) plus Kind — the module
// name the instance was built from (e.g. "xpath", "duckduckgo").
type Engine struct {
	Name       string
	Shortcut   string
	Categories []string
	Kind       string
}

// SearchEngine wraps an internal engine.Engine instance so facade
// consumers can hold a fully functional engine without ever seeing the
// internal interface. It implements engine.Engine by delegation and
// exposes the public metadata via Info.
//
// It is the adapter point of the facade: internal types never leak into
// the public API surface.
type SearchEngine struct {
	impl engine.Engine
	info Engine
}

// Compile-time assertion: SearchEngine satisfies the internal engine
// interface by delegation.
var _ engine.Engine = (*SearchEngine)(nil)

// NewSearchEngine wraps an internal engine instance into the public
// adapter. kind is the registered module name the instance was built
// from (e.g. "xpath").
func NewSearchEngine(impl engine.Engine, kind string) *SearchEngine {
	if impl == nil {
		return nil
	}
	return &SearchEngine{
		impl: impl,
		info: Engine{
			Name:       impl.Name(),
			Shortcut:   impl.Shortcut(),
			Categories: append([]string(nil), impl.Categories()...),
			Kind:       kind,
		},
	}
}

// Info returns the public metadata of the wrapped engine.
func (s *SearchEngine) Info() Engine { return s.info }

// Name delegates to the wrapped engine.
func (s *SearchEngine) Name() string { return s.impl.Name() }

// Shortcut delegates to the wrapped engine.
func (s *SearchEngine) Shortcut() string { return s.impl.Shortcut() }

// Categories delegates to the wrapped engine.
func (s *SearchEngine) Categories() []string { return s.impl.Categories() }

// NeedsInit delegates to the wrapped engine.
func (s *SearchEngine) NeedsInit() bool { return s.impl.NeedsInit() }

// Setup delegates to the wrapped engine.
func (s *SearchEngine) Setup(ctx context.Context, cfg *config.EngineConfig) error {
	return s.impl.Setup(ctx, cfg)
}

// Init delegates to the wrapped engine.
func (s *SearchEngine) Init(ctx context.Context) error { return s.impl.Init(ctx) }

// Request delegates to the wrapped engine.
func (s *SearchEngine) Request(query string, params *engine.RequestParams) error {
	return s.impl.Request(query, params)
}

// Response delegates to the wrapped engine.
func (s *SearchEngine) Response(resp *http.Response) ([]*result.RawResult, error) {
	return s.impl.Response(resp)
}
