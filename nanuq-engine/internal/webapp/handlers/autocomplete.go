package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"nanuq-engine/internal/autocomplete"
	"nanuq-engine/internal/config"
)

// autocompleteSearcher is the subset of the autocomplete registry the handler
// needs. It matches autocomplete.Search's signature exactly, so the real
// function can be injected directly; tests inject a fake to avoid network I/O.
type autocompleteSearcher func(backendName string, ctx context.Context, query string, locale string) ([]string, error)

// defaultAutocompleteBackend is used when cfg.Search.Autocomplete is empty
// (REQ-019: "duckduckgo" is the fallback provider).
const defaultAutocompleteBackend = "duckduckgo"

// RegisterAutocomplete wires the GET /autocompleter route (REQ-019,
// TASK-013), porting webapp.py autocompleter().
//
// The handler needs cfg.Search.Autocomplete (the configured backend name)
// and the autocomplete package for the actual lookup. Network access goes
// through the autocomplete package's own http.Client in this phase.
//
// TODO(TASK-022): once the autocomplete registry is wired to the search
// configuration/catalog, this handler should resolve the backend through the
// same plumbing instead of calling autocomplete.Search directly.
func RegisterAutocomplete(mux *http.ServeMux, cfg *config.Config) {
	mux.HandleFunc("/autocompleter", autocompleteHandler(cfg, autocomplete.Search))
}

// autocompleteHandler serves GET /autocompleter.
//
// It mirrors the upstream webapp.py autocompleter():
//
//   - an empty q returns an empty JSON list;
//   - an X-Requested-With: XMLHttpRequest header gets a flat JSON array of
//     suggestion strings (Content-Type application/json);
//   - otherwise the response is the OpenSearch suggestion format (CA-007):
//     [prefix, results, [], [], {"google:suggestrelevance": [600-i, ...]}]
//     with Content-Type application/x-suggestions+json.
//
// A failing backend degrades to an empty suggestion list (200), matching the
// upstream search_autocomplete behaviour of swallowing backend errors; the
// autocompleter must never break the search page.
func autocompleteHandler(cfg *config.Config, search autocompleteSearcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.FormValue("q")
		if query == "" {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write([]byte("[]"))
			return
		}

		backendName := cfg.Search.Autocomplete
		if backendName == "" {
			backendName = defaultAutocompleteBackend
		}

		results, err := search(backendName, r.Context(), query, r.FormValue("locale"))
		if err != nil {
			// Mirror search_autocomplete (autocomplete.py L416-423): a
			// failing backend yields no suggestions, never an error page.
			slog.Error("autocompleter: backend search failed", "backend", backendName, "query", query, "err", err)
			results = []string{}
		}
		if results == nil {
			results = []string{}
		}

		if r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_ = json.NewEncoder(w).Encode(results)
			return
		}

		relevance := make([]int, len(results))
		for i := range relevance {
			relevance[i] = 600 - i
		}
		payload := []any{
			query,
			results,
			[]string{},
			[]string{},
			map[string]any{"google:suggestrelevance": relevance},
		}
		w.Header().Set("Content-Type", "application/x-suggestions+json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(payload)
	}
}
