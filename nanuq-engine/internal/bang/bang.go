// Package bang provides the in-memory store for the external bangs dataset
// (SearXNG / DuckDuckGo bang definitions) used to redirect searches that
// carry a "!!bang" prefix.
//
// It implements DSG-016 / REQ-020: the serialized bang trie is flattened at
// load time into a plain map[string]BangDef (DECISION-012 — a map with ~19k
// entries is sufficient; no trie is kept in memory).
package bang

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Control characters used by the serialized bang dataset (SearXNG format).
const (
	// leafKey (chr(16)) is the dict key holding a leaf definition inside a
	// trie node that also has child keys extending the bang name.
	leafKey = "\x10"
	// sepByte (chr(1)) separates the URL template from the rank in a leaf
	// definition string ("url" + chr(1) + "rank").
	sepByte = "\x01"
	// queryMarker (chr(2)) is the placeholder inside a URL template that is
	// replaced by the URL-escaped query at resolution time.
	queryMarker = "\x02"
)

// bangsData bundles the third-party dataset shipped with the module
// (see internal/bang/data/README.md for provenance and license terms).
//
//go:embed data
var bangsData embed.FS

// BangDef is a single bang definition: a URL template plus a popularity rank.
type BangDef struct {
	// URL is the search URL template. It may contain the query marker
	// (chr(2), "\x02") which GetBangURL replaces with the URL-escaped query,
	// and it may start with "//", meaning the "https:" scheme is implied.
	URL string
	// Rank is the popularity rank of the bang (higher is more popular).
	Rank int
}

// BangStore is the read-only interface consumed by the query parser
// (TASK-005). Defining it here decouples consumers from the concrete
// *Store implementation.
//
// Bang names are passed without the leading "!" prefix.
type BangStore interface {
	Lookup(name string) (BangDef, bool)
}

// Compile-time check that *Store satisfies the BangStore contract.
var _ BangStore = (*Store)(nil)

// Store is an in-memory map of bang names to their definitions.
//
// The Store is intended to be loaded once at startup and then read
// concurrently; Load is not safe for concurrent use.
type Store struct {
	bangs map[string]BangDef
}

// New returns an empty Store, ready to be populated with Load or
// LoadEmbedded.
func New() *Store {
	return &Store{bangs: make(map[string]BangDef)}
}

// LoadEmbedded loads the bundled dataset (internal/bang/data/
// external_bangs.json) into the store. It returns an error if the embedded
// data cannot be read or parsed.
func (s *Store) LoadEmbedded() error {
	data, err := bangsData.ReadFile("data/external_bangs.json")
	if err != nil {
		return fmt.Errorf("bang: read embedded dataset: %w", err)
	}
	return s.Load(data)
}

// Load parses a serialized bang dataset (SearXNG external_bangs.json format)
// and populates the store, replacing any previously loaded definitions.
//
// The dataset is a prefix-compressed trie serialized as nested JSON maps
// under a top-level "trie" key. A node value is either:
//
//   - a leaf definition string: "url" + chr(1) + "rank"; or
//   - a nested map, which may carry its own leaf definition under the
//     LEAF_KEY (chr(16)) entry plus child keys that extend the bang name.
//
// The bang name is the concatenation of all keys along the path from the
// trie root. The trie is flattened here into a plain map[string]BangDef
// (DECISION-012). Bang names are stored without the "!" prefix.
func (s *Store) Load(data []byte) error {
	var root struct {
		Trie map[string]any `json:"trie"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("bang: parse dataset: %w", err)
	}
	if root.Trie == nil {
		return fmt.Errorf("bang: dataset missing \"trie\" key")
	}

	flattened := make(map[string]BangDef, len(root.Trie))
	if err := flattenTrie(root.Trie, "", flattened); err != nil {
		return err
	}
	s.bangs = flattened
	return nil
}

// flattenTrie walks a trie node recursively, storing every leaf definition
// under the bang name accumulated by concatenating the keys along the path.
func flattenTrie(node map[string]any, prefix string, bangs map[string]BangDef) error {
	for key, value := range node {
		// LEAF_KEY is metadata consumed by the frame that owns this dict
		// node; it never extends the bang name, so it must not be walked.
		if key == leafKey {
			continue
		}
		name := prefix + key
		switch v := value.(type) {
		case string:
			def, err := parseDefinition(name, v)
			if err != nil {
				return err
			}
			bangs[name] = def
		case map[string]any:
			if leaf, ok := v[leafKey]; ok {
				leafStr, ok := leaf.(string)
				if !ok {
					return fmt.Errorf("bang: node %q: LEAF_KEY value is not a string", name)
				}
				def, err := parseDefinition(name, leafStr)
				if err != nil {
					return err
				}
				bangs[name] = def
			}
			if err := flattenTrie(v, name, bangs); err != nil {
				return err
			}
		default:
			return fmt.Errorf("bang: node %q: unexpected value type %T", name, value)
		}
	}
	return nil
}

// parseDefinition parses a leaf definition string of the form
// "url" + chr(1) + "rank" into a BangDef. An empty rank yields Rank 0,
// mirroring SearXNG's resolve_bang_definition.
func parseDefinition(name, definition string) (BangDef, error) {
	parts := strings.Split(definition, sepByte)
	if len(parts) != 2 {
		return BangDef{}, fmt.Errorf(
			"bang: definition for %q: expected \"url\" + chr(1) + \"rank\", got %d fields", name, len(parts))
	}
	rank := 0
	if rankStr := parts[1]; rankStr != "" {
		var err error
		rank, err = strconv.Atoi(rankStr)
		if err != nil {
			return BangDef{}, fmt.Errorf("bang: definition for %q: invalid rank %q: %w", name, rankStr, err)
		}
	}
	return BangDef{URL: parts[0], Rank: rank}, nil
}

// Lookup returns the definition of the bang named name, without the "!"
// prefix. The second return value is false when the bang is unknown.
func (s *Store) Lookup(name string) (BangDef, bool) {
	def, ok := s.bangs[name]
	return def, ok
}

// Len returns the number of bangs currently stored.
func (s *Store) Len() int {
	return len(s.bangs)
}

// GetBangURL resolves the bang named name with the given query and returns
// the final search URL. The second return value is false when the bang is
// unknown.
//
// Resolution mirrors SearXNG's resolve_bang_definition:
//   - a URL starting with "//" is prefixed with "https:";
//   - a non-empty query replaces the query marker (chr(2)) with
//     url.QueryEscape(query);
//   - an empty query falls back to the site's main page (scheme://netloc).
func (s *Store) GetBangURL(name, query string) (string, bool) {
	def, ok := s.Lookup(name)
	if !ok {
		return "", false
	}
	return resolveURL(def.URL, query), true
}

// resolveURL interpolates the query into a URL template (ported from
// SearXNG's resolve_bang_definition).
func resolveURL(template, query string) string {
	u := template
	if strings.HasPrefix(u, "//") {
		u = "https:" + u
	}
	if query != "" {
		return strings.ReplaceAll(u, queryMarker, url.QueryEscape(query))
	}
	return mainPage(u)
}

// mainPage reduces a URL template to "scheme://host" (the site's main page),
// mirroring SearXNG's empty-query behavior. It deliberately avoids url.Parse:
// the template may still contain the chr(2) query marker, which is an
// invalid control character in a URL and would make Parse fail.
func mainPage(u string) string {
	rest := u
	scheme := ""
	if i := strings.Index(u, "://"); i >= 0 {
		scheme = u[:i]
		rest = u[i+len("://"):]
	}
	if j := strings.IndexAny(rest, "/?"); j >= 0 {
		rest = rest[:j]
	}
	if scheme != "" && rest != "" {
		return scheme + "://" + rest
	}
	return u
}
