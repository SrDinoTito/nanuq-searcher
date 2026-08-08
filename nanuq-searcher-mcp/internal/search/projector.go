// Package search bridges the nanuq-engine facade and the clean MCP domain
// (DSG-003, DSG-004).
package search

import "nanuq-searcher-mcp/internal/domain"

// Clean result keys read by the projector (REQ-003). Everything else in a
// SearXNG dict — parsed_url, template, priority, thumbnail, img_src,
// positions — is junk for an agent and is deliberately never read.
const (
	keyTitle    = "title"
	keyContent  = "content"
	keyURL      = "url"
	keyEngines  = "engines"
	keyScore    = "score"
	keyCategory = "category"
)

// Project converts one SearXNG-style result dict (produced by
// MainResult.AsDict()) into a clean domain.SearchHit (DSG-004, REQ-003).
//
// The projection is defensive: a missing key or an unexpected type yields a
// zero value ("" / nil / 0) and never panics. Junk keys (parsed_url,
// template, priority, thumbnail, img_src, positions) are never read, so they
// are never propagated into the clean type.
func Project(raw map[string]any) domain.SearchHit {
	if raw == nil {
		return domain.SearchHit{}
	}
	return domain.SearchHit{
		Title:    asString(raw[keyTitle]),
		Content:  asString(raw[keyContent]),
		URL:      asString(raw[keyURL]),
		Engines:  asStrings(raw[keyEngines]),
		Score:    asFloat(raw[keyScore]),
		Category: asString(raw[keyCategory]),
	}
}

// ProjectMany converts an ordered slice of result dicts into clean hits,
// preserving the order (REQ-003). A nil input yields nil.
func ProjectMany(raws []map[string]any) []domain.SearchHit {
	if raws == nil {
		return nil
	}
	hits := make([]domain.SearchHit, 0, len(raws))
	for _, raw := range raws {
		hits = append(hits, Project(raw))
	}
	return hits
}

// asString extracts a string value; any other type or absence yields "".
func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// asStrings extracts []string from either []string or []any (tolerating
// mixed elements: non-string entries are skipped). Absence or any other
// type yields nil.
func asStrings(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// asFloat extracts float64 from float64, int or int64 (the engine emits
// float64, but a JSON round-trip may deliver int); absence or any other
// type yields 0.
func asFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case int64:
		return float64(t)
	default:
		return 0
	}
}
