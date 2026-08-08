package autocomplete

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/url"
	"strings"
)

// bingCVID generates a 32-character client ID for the Bing suggest API.
// The Python reference (autocomplete.py L65) builds it from
// string.ascii_uppercase + string.digits; math/rand replaces the upstream
// random.choices (deterministic seed is irrelevant here — the value is an
// opaque correlation id).
func bingCVID() string {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 32)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

// bingBackend ports autocomplete.py bing() (L61-78):
//
//	url = 'https://www.bing.com/AS/Suggestions?' + urlencode({'qry': query, 'csr': 1, 'cvid': cvid})
//	data = resp.json()          # dict
//	if 's' in data:
//	    for item in data['s']:
//	        completion = item['q'].replace('\ue000', '').replace('\ue001', '')
//
// Bing embeds the suggestions in a dict under the 's' key, and uses the
// private-use Unicode characters U+E000/U+E001 to highlight query parts;
// they are stripped the same way as upstream.
func bingBackend(baseURL string) Backend {
	return func(ctx context.Context, query string, _ string) ([]string, error) {
		args := url.Values{}
		args.Set("qry", query)
		args.Set("csr", "1")
		args.Set("cvid", bingCVID())
		body, err := doGet(ctx, baseURL+"?"+args.Encode(), nil)
		if err != nil {
			return nil, fmt.Errorf("autocomplete: bing: %w", err)
		}

		var data map[string]json.RawMessage
		if err := json.Unmarshal(body, &data); err != nil {
			return nil, fmt.Errorf("autocomplete: bing: decode response: %w", err)
		}
		s, ok := data["s"]
		if !ok {
			return []string{}, nil
		}

		var items []map[string]json.RawMessage
		if err := json.Unmarshal(s, &items); err != nil {
			return nil, fmt.Errorf("autocomplete: bing: decode suggestions: %w", err)
		}

		results := []string{}
		for _, item := range items {
			var rawQ json.RawMessage
			// item["q"] may be missing or malformed; tolerate it like the
			// upstream dict.get-style access.
			rawQ = item["q"]
			var completion string
			if err := json.Unmarshal(rawQ, &completion); err != nil {
				continue
			}
			completion = strings.ReplaceAll(completion, "\ue000", "")
			completion = strings.ReplaceAll(completion, "\ue001", "")
			results = append(results, completion)
		}
		return results, nil
	}
}
