package autocomplete

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// duckduckgoBackend ports autocomplete.py duckduckgo() (L109-126):
//
//	url = 'https://duckduckgo.com/ac/?type=list&' + urlencode({'q': query, 'kl': ...})
//	j = resp.json()
//	if len(j) > 1: results = j[1]
//
// The response is the DuckDuckGo suggestion list: a JSON array whose first
// element is the query prefix and whose second is the list of suggestions.
//
// Deviation from the Python reference: the upstream derives the 'kl' (region)
// parameter from the locale via ENGINE_TRAITS
// (traits.get_region(sxng_locale, traits.all_locale)). Traits are not ported
// in this task, so 'kl' is always "wt-wt" — the same "All regions" value the
// internal/engines/duckduckgo.go engine sends without trait support. The
// locale parameter is accepted for signature parity but unused; region
// support arrives with the trait tables (TASK-022).
func duckduckgoBackend(baseURL string) Backend {
	return func(ctx context.Context, query string, _ string) ([]string, error) {
		args := url.Values{}
		args.Set("q", query)
		args.Set("kl", "wt-wt")
		body, err := doGet(ctx, baseURL+"/?type=list&"+args.Encode(), nil)
		if err != nil {
			return nil, fmt.Errorf("autocomplete: duckduckgo: %w", err)
		}

		var j []json.RawMessage
		if err := json.Unmarshal(body, &j); err != nil {
			return nil, fmt.Errorf("autocomplete: duckduckgo: decode response: %w", err)
		}
		if len(j) <= 1 {
			return []string{}, nil
		}

		results := []string{}
		if err := json.Unmarshal(j[1], &results); err != nil {
			return nil, fmt.Errorf("autocomplete: duckduckgo: decode suggestions: %w", err)
		}
		return results, nil
	}
}
