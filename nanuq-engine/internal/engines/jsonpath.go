// jsonpath.go ports the mini JSON-path language of SearXNG's json_engine.py
// (parse / do_query / iterate / is_iterable, L264-315) by hand.
//
// CON-004 / DECISION-007: this mini-language is part of the engine behaviour
// contract and MUST NOT be replaced by an external JSONPath / JMESPath
// library — the manual port is the highest functional risk of the spec. The
// reference line comments below point at json_engine.py.
//
// Semantics (see Query): a path is a slash-separated list of key tokens.
// Objects are descended directly by key; arrays are either selected by index
// token ("0", "1", ...), iterated token-wise ("[]"), or traversed by applying
// the remaining tokens to every element and concatenating the matches —
// json_engine.py results_query docstring: "Array entries can be specified
// using the index or can be omitted entirely, in which case each entry is
// considered".
package engines

import (
	"strconv"
	"strings"
)

// Parse splits a JSON path on '/' and drops empty segments — a direct port of
// json_engine.py parse() (L280-286): `for part in query.split('/'): if part
// == ”: continue; q.append(part)`.
//
// The "[]" token is kept as-is; Query interprets it against array nodes.
func Parse(path string) []string {
	tokens := make([]string, 0, 8)
	for _, part := range strings.Split(path, "/") {
		if part == "" {
			continue
		}
		tokens = append(tokens, part)
	}
	return tokens
}

// IsIterable reports whether data is a JSON object or array — a port of
// json_engine.py is_iterable() (L274-277). Unlike Python, Go strings are
// never iterable here.
func IsIterable(data any) bool {
	switch data.(type) {
	case map[string]any, []any:
		return true
	}
	return false
}

// Query descends tokens into data and reports whether the path matched.
//
// It ports json_engine.py query()/do_query() (L289-315) with the semantics
// fixed by TASK-010: direct descent by key over objects (no Python-style
// deep-search of sibling branches), array nodes iterated with the remaining
// tokens (matches concatenated into a single []any), array index tokens
// ("0") selecting a single entry, and the "[]" token meaning "each entry".
//
// A match is reported as (value, true); a missing key, a scalar reached with
// tokens still pending, or an empty token list reports (nil, false).
func Query(data any, tokens []string) (any, bool) {
	if len(tokens) == 0 {
		return nil, false
	}
	return queryNode(data, tokens)
}

// queryNode performs one descent step (json_engine.py do_query, L296-308).
func queryNode(node any, tokens []string) (any, bool) {
	if len(tokens) == 0 {
		return node, true
	}

	switch n := node.(type) {
	case map[string]any:
		// json_engine.py do_query: `if key == qkey` — direct object lookup,
		// then the rest of the path.
		next, ok := n[tokens[0]]
		if !ok {
			return nil, false
		}
		return queryNode(next, tokens[1:])

	case []any:
		tok := tokens[0]
		if tok == "[]" {
			// "[]" means "each entry": the array itself when nothing follows,
			// otherwise every element matched with the remaining tokens.
			if len(tokens) == 1 {
				return node, true
			}
			return iterateArray(n, tokens[1:])
		}
		// json_engine.py results_query docstring: "Array entries can be
		// specified using the index".
		if idx, err := strconv.Atoi(tok); err == nil {
			if idx < 0 || idx >= len(n) {
				return nil, false
			}
			return queryNode(n[idx], tokens[1:])
		}
		// json_engine.py do_query: "each entry is considered" — the remaining
		// tokens are applied to every element and the matches concatenated.
		return iterateArray(n, tokens)

	default:
		// Scalars are not iterable (is_iterable L274-277): a pending token
		// cannot match.
		return nil, false
	}
}

// iterateArray applies tokens to each element of arr and concatenates the
// matches — json_engine.py do_query `ret.extend(...)` (L298-308). A single
// match is unwrapped, mirroring `query(result, q)[0]` used by
// extract_response_info.
func iterateArray(arr []any, tokens []string) (any, bool) {
	var matched []any
	for _, el := range arr {
		if v, ok := queryNode(el, tokens); ok {
			matched = append(matched, v)
		}
	}
	switch len(matched) {
	case 0:
		return nil, false
	case 1:
		return matched[0], true
	default:
		return matched, true
	}
}
