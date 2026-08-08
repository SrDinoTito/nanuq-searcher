package search

import (
	"context"
	"errors"
	"fmt"

	nanuq "nanuq-engine/pkg/nanuq"

	"nanuq-searcher-mcp/internal/config"
	"nanuq-searcher-mcp/internal/domain"
)

// Adapter bridges the nanuq-engine facade (nanuq.Service) and the clean
// MCP search domain (DSG-003/DSG-004). It owns the translation between
// the engine's raw map results and domain.SearchHit via the Projectors.
type Adapter struct {
	svc *nanuq.Service
}

// NewAdapter wraps a nanuq.Service facade. The service is used lazily, so
// a nil service is only rejected at call time (Search).
func NewAdapter(svc *nanuq.Service) *Adapter {
	return &Adapter{svc: svc}
}

// Search runs a query through the nanuq facade and projects the raw
// engine results into domain.SearchResult.
//
// Categories: the engine's query parser supports the SearXNG bang syntax
// (`!category`) — evidence: nanuq-engine/internal/search/query.go
// (callBangParser resolves `!x` tokens via catalog.EnginesInCategory) and
// nanuq-engine/internal/search/search.go (buildSearchQuery turns the
// parsed EngineRefs into engine requests). The facade does not expose the
// catalog for pre-validation, so each non-empty category is injected as a
// "!<cat>" prefix token before the facade call; an unknown category
// degrades into plain query words without error. Because the engine
// supports this syntax, no "categories not supported" fallback is needed.
//
// maxResults caps the projected hits; maxResults <= 0 falls back to
// config.DefaultSearchMaxResults (REQ-002).
func (a *Adapter) Search(ctx context.Context, query string, categories []string, maxResults int) (*domain.SearchResult, error) {
	if a == nil || a.svc == nil {
		return nil, errors.New("search: nil nanuq service")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	if maxResults <= 0 {
		maxResults = config.DefaultSearchMaxResults
	}

	res, err := a.svc.Search(withCategoryPrefix(query, categories))
	if err != nil {
		return nil, fmt.Errorf("search: nanuq engine: %w", err)
	}

	hits := truncateHits(ProjectMany(res.Results), maxResults)
	return mapResult(res, hits), nil
}

// withCategoryPrefix prepends a "!<cat>" bang token for each non-empty
// category, so the engine restricts the search to those categories.
// Empty categories and a nil/empty slice leave the query unchanged.
func withCategoryPrefix(query string, categories []string) string {
	prefix := ""
	for _, cat := range categories {
		if cat == "" {
			continue
		}
		prefix += "!" + cat + " "
	}
	return prefix + query
}

// truncateHits caps hits at maxResults preserving order. A maxResults <= 0
// leaves hits unchanged (the caller has already applied the default).
func truncateHits(hits []domain.SearchHit, maxResults int) []domain.SearchHit {
	if maxResults > 0 && len(hits) > maxResults {
		return hits[:maxResults]
	}
	return hits
}

// mapResult maps the engine facade Result onto the clean domain type.
// A nil result still yields a non-nil domain result with the given hits.
func mapResult(res *nanuq.Result, hits []domain.SearchHit) *domain.SearchResult {
	out := &domain.SearchResult{Hits: hits}
	if res == nil {
		return out
	}
	out.Query = res.Query
	out.Unresponsive = res.Unresponsive
	out.RedirectURL = res.RedirectURL
	return out
}
