package autocomplete

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"strings"
)

// googleCompleteBackend ports autocomplete.py google_complete() (L129-157):
//
//	args = urlencode({'q': query, 'client': 'gws-wiz', 'hl': google_info['params']['hl']})
//	url = 'https://{subdomain}/complete/search?{args}'
//	json_txt = resp.text[resp.text.find('[') : resp.text.find(']', -3) + 1]
//	data = json.loads(json_txt)
//	for item in data[0]:
//	    results.append(lxml.html.fromstring(item[0]).text_content())
//
// The response is the Google suggest JSONP payload; the upstream cuts the
// first '[' .. last ']' pair out of the text and parses that slice as JSON.
// The top-level array's element 0 is the list of suggestions, and each
// suggestion is itself a list whose first element is the suggestion text,
// possibly carrying HTML markup (e.g. <b>query</b> highlights).
//
// Deviations from the Python reference:
//
//   - The subdomain and 'hl' value come from google.get_google_info() +
//     ENGINE_TRAITS (region/language tables, not ported). This port always
//     uses the default subdomain www.google.com and derives 'hl' from the
//     locale parameter (falling back to "en"); per-language subdomains
//     arrive with the trait tables (TASK-022).
//
//   - Python's resp.text.find(']', -3) finds the last ']' before the tail of
//     the payload; the Go equivalent bytes.LastIndexByte(body, ']') is used
//     here, which is strictly more robust (it locates the true final ']'
//     regardless of trailing whitespace).
//
//   - The upstream strips markup with lxml.html.fromstring(item[0]).text_content().
//     Without lxml, tags are removed with a small stdlib stripper and
//     entities decoded via html.UnescapeString (stdlib "html" package) —
//     equivalent text extraction for the simple markup Google emits.
func googleCompleteBackend(baseURL string) Backend {
	return func(ctx context.Context, query string, locale string) ([]string, error) {
		hl := locale
		if hl == "" {
			hl = "en"
		}
		args := url.Values{}
		args.Set("q", query)
		args.Set("client", "gws-wiz")
		args.Set("hl", hl)
		body, err := doGet(ctx, baseURL+"?"+args.Encode(), nil)
		if err != nil {
			return nil, fmt.Errorf("autocomplete: google_complete: %w", err)
		}

		start := bytes.IndexByte(body, '[')
		end := bytes.LastIndexByte(body, ']')
		if start < 0 || end <= start {
			return nil, fmt.Errorf("autocomplete: google_complete: response has no [..] JSON array")
		}

		var data []json.RawMessage
		if err := json.Unmarshal(body[start:end+1], &data); err != nil {
			return nil, fmt.Errorf("autocomplete: google_complete: decode response: %w", err)
		}
		if len(data) == 0 {
			return []string{}, nil
		}

		var items []json.RawMessage
		if err := json.Unmarshal(data[0], &items); err != nil {
			return nil, fmt.Errorf("autocomplete: google_complete: decode suggestions: %w", err)
		}

		results := []string{}
		for _, item := range items {
			var fields []any
			if err := json.Unmarshal(item, &fields); err != nil {
				continue // skip malformed suggestion entries (mirrors upstream tolerance)
			}
			if len(fields) == 0 {
				continue
			}
			text, ok := fields[0].(string)
			if !ok {
				continue
			}
			results = append(results, stripHTMLTags(text))
		}
		return results, nil
	}
}

// stripHTMLTags removes markup from a suggestion string and decodes HTML
// entities, replacing lxml.html.fromstring(...).text_content() for the small
// set of tags Google's suggest API emits (<b> highlights and friends).
func stripHTMLTags(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	return html.UnescapeString(b.String())
}
