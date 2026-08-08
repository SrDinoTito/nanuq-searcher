package autocomplete

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// wikipediaBackend ports autocomplete.py wikipedia() (L355-379):
//
//	args = urlencode({'action': 'opensearch', 'format': 'json', 'formatversion': '2',
//	                 'search': query, 'namespace': '0', 'limit': '10'})
//	resp = get(f'https://{wiki_netloc}/w/api.php?{args}')
//	data = resp.json()
//	if len(data) > 1: results = data[1]
//
// MediaWiki opensearch returns a JSON array whose element 1 is the list of
// suggestion strings.
//
// Deviation from the Python reference: the upstream maps the locale to a
// language and then to a wiki_netloc via ENGINE_TRAITS custom data
// (traits.custom['wiki_netloc'], default 'en.wikipedia.org'). Traits are not
// ported, so the fixed default https://en.wikipedia.org is used; the locale
// parameter is accepted for signature parity but unused. Per-language
// netloc mapping arrives with the trait tables (TASK-022).
func wikipediaBackend(baseURL string) Backend {
	return func(ctx context.Context, query string, _ string) ([]string, error) {
		args := url.Values{}
		args.Set("action", "opensearch")
		args.Set("format", "json")
		args.Set("formatversion", "2")
		args.Set("search", query)
		args.Set("namespace", "0")
		args.Set("limit", "10")
		body, err := doGet(ctx, baseURL+"?"+args.Encode(), nil)
		if err != nil {
			return nil, fmt.Errorf("autocomplete: wikipedia: %w", err)
		}

		var data []json.RawMessage
		if err := json.Unmarshal(body, &data); err != nil {
			return nil, fmt.Errorf("autocomplete: wikipedia: decode response: %w", err)
		}
		if len(data) <= 1 {
			return []string{}, nil
		}

		results := []string{}
		if err := json.Unmarshal(data[1], &results); err != nil {
			return nil, fmt.Errorf("autocomplete: wikipedia: decode suggestions: %w", err)
		}
		return results, nil
	}
}
