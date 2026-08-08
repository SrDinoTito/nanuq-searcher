package nanuq

import (
	"fmt"
	"strings"

	"nanuq-engine/internal/bang"
	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
	"nanuq-engine/internal/network"
	"nanuq-engine/internal/search"
)

// Service is the public search entry point of the facade (DSG-018): it
// wires the full internal pipeline — bang store, engine catalog, network
// requester and SearchService — and exposes a clean, HTTP-free Search API.
type Service struct {
	svc     *search.SearchService
	bang    *bang.Store
	catalog search.EngineCatalog
	cfg     *config.Config
}

// Result is the public search result of the facade. Results holds one
// map per ordered main result, produced by result.MainResult.AsDict
// (SearXNG-style JSON keys: title, content, url, engines, score, ...) —
// deliberately JSON-friendly for MCP consumers instead of leaking the
// internal result types.
type Result struct {
	// Query is the plain user query (bang/language/timeout tokens removed).
	Query string
	// Results is the ordered list of main results as dicts.
	Results []map[string]any
	// Unresponsive lists the engine names that timed out or failed.
	Unresponsive []string
	// RedirectURL is set when the query resolved to an external bang
	// ("!!name") or a redirect; empty otherwise.
	RedirectURL string
}

// NewService builds the search pipeline for cfg and reg. It loads the
// embedded bang dataset, derives the engine catalog from cfg.Engines and
// creates the network requester. answerers are not wired (nil).
//
// The engine catalog is derived per the search package contract: all keys
// are lowercase, and the "all" category lists every enabled engine
// (SearXNG convention) — the standard search only fires requests when the
// catalog knows the "all" category.
func NewService(cfg *config.Config, reg *engine.Registry) (*Service, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nanuq: nil config")
	}
	if reg == nil {
		return nil, fmt.Errorf("nanuq: nil engine registry")
	}

	store := bang.New()
	if err := store.LoadEmbedded(); err != nil {
		return nil, fmt.Errorf("nanuq: load embedded bangs: %w", err)
	}

	catalog := newCatalog(reg, cfg.Engines)

	requester, err := network.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("nanuq: network requester: %w", err)
	}

	svc := search.New(reg, store, catalog, cfg, requester, nil)
	return &Service{svc: svc, bang: store, catalog: catalog, cfg: cfg}, nil
}

// Search runs the pipeline for a plain query string: parse → search →
// close (score) → ordered results. The returned Result is never nil; an
// empty query yields an empty Result without panicking.
func (s *Service) Search(query string) (*Result, error) {
	rtq := search.Parse(query, s.bang, s.catalog)
	rc := s.svc.Search(rtq)
	rc.Close(1.0, nil)

	res := &Result{
		Query:        rtq.GetQuery(),
		Results:      make([]map[string]any, 0),
		Unresponsive: make([]string, 0),
		RedirectURL:  rc.RedirectURL(),
	}
	for _, m := range rc.GetOrderedResults() {
		res.Results = append(res.Results, m.AsDict())
	}
	for _, u := range rc.Unresponsive() {
		res.Unresponsive = append(res.Unresponsive, u.Name)
	}
	return res, nil
}

// Bangs resolves an external bang ("!!name") in query to its redirect
// URL. The returned bool is false when query carries no known external
// bang.
func (s *Service) Bangs(query string) (string, bool) {
	rtq := search.Parse(query, s.bang, s.catalog)
	if rtq.ExternalBang == "" {
		return "", false
	}
	return s.bang.GetBangURL(rtq.ExternalBang, rtq.GetQuery())
}

// newCatalog derives a search.RegistryCatalog from the configured engine
// instances (skipping Disabled/Inactive), mirroring the wiring TASK-006
// expects from the caller (query.go: RegistryCatalog bridges the Registry
// with caller-injected shortcut/category maps). All keys are lowercase.
func newCatalog(reg *engine.Registry, engines []config.EngineConfig) *search.RegistryCatalog {
	cat := &search.RegistryCatalog{
		Reg:        reg,
		Shortcuts:  make(map[string]string),
		Engines:    make(map[string]bool),
		Categories: make(map[string][]string),
	}
	for i := range engines {
		ecfg := engines[i]
		if ecfg.Disabled || ecfg.Inactive {
			continue
		}
		name := strings.ToLower(ecfg.Name)
		if name == "" {
			continue
		}
		cat.Engines[name] = true
		if sc := strings.ToLower(ecfg.Shortcut); sc != "" {
			cat.Shortcuts[sc] = name
		}
		for _, category := range ecfg.Categories {
			if c := strings.ToLower(category); c != "" {
				cat.Categories[c] = append(cat.Categories[c], name)
			}
		}
		// "all" is the SearXNG convention: every enabled engine.
		cat.Categories["all"] = append(cat.Categories["all"], name)
	}
	return cat
}
