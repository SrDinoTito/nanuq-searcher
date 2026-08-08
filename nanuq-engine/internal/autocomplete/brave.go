package autocomplete

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// braveBackend ports autocomplete.py brave() (L81-94):
//
//	url = 'https://search.brave.com/api/suggest?' + urlencode({'q': query})
//	kwargs = {'cookies': {'country': 'all'}}
//	data = resp.json()          # [[...], [sug1, sug2, ...]]
//	for item in data[1]: results.append(item)
//
// The response is a JSON array whose element 1 is the list of suggestions.
// The upstream sends a country=all cookie; the Go port sets it via
// req.AddCookie (the http.Client has no cookie jar configured).
func braveBackend(baseURL string) Backend {
	return func(ctx context.Context, query string, _ string) ([]string, error) {
		args := url.Values{}
		args.Set("q", query)
		body, err := doGet(ctx, baseURL+"?"+args.Encode(), func(req *http.Request) {
			req.AddCookie(&http.Cookie{Name: "country", Value: "all"})
		})
		if err != nil {
			return nil, fmt.Errorf("autocomplete: brave: %w", err)
		}

		var data []json.RawMessage
		if err := json.Unmarshal(body, &data); err != nil {
			return nil, fmt.Errorf("autocomplete: brave: decode response: %w", err)
		}
		if len(data) <= 1 {
			return []string{}, nil
		}

		results := []string{}
		if err := json.Unmarshal(data[1], &results); err != nil {
			return nil, fmt.Errorf("autocomplete: brave: decode suggestions: %w", err)
		}
		return results, nil
	}
}
