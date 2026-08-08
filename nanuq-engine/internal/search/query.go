// Package search implements the query parser (TASK-005, REQ-002): it
// translates the raw search input into a structured RawTextQuery, mirroring
// SearXNG's searx/query.py. The parser is incremental: every
// whitespace-separated token is offered to a fixed sequence of parsers
// (PARSER_CLASSES), and the first parser whose check matches claims the
// token. Tokens consumed by a parser are "special" query parts (bangs,
// language, timeout); everything else stays in the plain user query.
//
// The package is parse-only: it must not be integrated with the
// SearchService (TASK-006) or the web adapter (TASK-012). Those layers
// consume RawTextQuery / SearchQuery through the interfaces defined here.
package search

import (
	"regexp"
	"strconv"
	"strings"

	"nanuq-engine/internal/bang"
	"nanuq-engine/internal/engine"
)

// EngineRef references a single engine, optionally scoped to a category
// (SearXNG search/models.py EngineRef). The Category field is "none" for a
// plain engine reference — faithful to query.py L200, which always uses
// EngineRef(value, 'none') for engine names — or a real category name when
// the reference was produced by expanding a category bang (query.py L207-211).
type EngineRef struct {
	Name     string
	Category string
}

// SearchQuery is the structured, service-ready form of a search request
// (DSG-002). It is the input contract of the SearchService (TASK-006); the
// parser package only defines it. Default conventions: Lang is "all",
// SafeSearch is 0, Pageno is 1 and TimeRange is "" (no filter) unless
// TASK-006 overrides them.
type SearchQuery struct {
	Query                 string
	EngineRefs            []EngineRef
	Lang                  string
	SafeSearch            int
	Pageno                int
	TimeRange             string
	TimeoutLimit          *float64
	ExternalBang          string
	Specific              bool
	RedirectToFirstResult bool
}

// RawTextQuery is the raw parsed form of the query input, mirroring
// SearXNG's RawTextQuery (searx/query.py L250). Field names match the spec
// (TASK-005), including the pythonic "Enginerefs" spelling.
type RawTextQuery struct {
	Query                 string
	UserQueryParts        []string
	QueryParts            []string
	Enginerefs            []EngineRef
	Languages             []string
	TimeoutLimit          *float64
	ExternalBang          string
	Specific              bool
	RedirectToFirstResult bool
}

// GetQuery returns the plain user query (query.py getQuery, L323-324):
// the special query parts are excluded.
func (q *RawTextQuery) GetQuery() string {
	return strings.Join(q.UserQueryParts, " ")
}

// GetFullQuery returns the full query including the special parts
// (query.py getFullQuery, L326-330).
func (q *RawTextQuery) GetFullQuery() string {
	parts := make([]string, 0, len(q.QueryParts)+len(q.UserQueryParts))
	parts = append(parts, q.QueryParts...)
	parts = append(parts, q.UserQueryParts...)
	return strings.TrimSpace(strings.Join(parts, " "))
}

// EngineCatalog supplies the engine information the BangParser needs:
// engine-name lookup, shortcut resolution and category expansion.
//
// The concrete *engine.Registry cannot serve this directly (TASK-005
// decision, documented for TASK-006): Registry exposes MODULE names only
// (registry.Has), while query.py matches ENGINE INSTANCE names, shortcuts
// and categories (query.py L193-214). RegistryCatalog bridges the two by
// combining *engine.Registry with maps injected by the caller; the tests
// use a fake catalog. All keys are lowercase.
type EngineCatalog interface {
	// Has reports whether name is a known engine (instance or module) name.
	Has(name string) bool

	// ResolveShortcut maps an engine shortcut to its engine name. The second
	// return value is false when shortcut is unknown.
	ResolveShortcut(shortcut string) (string, bool)

	// EnginesInCategory returns the ENABLED engine names declared under
	// category and reports whether the category exists at all. A category
	// may exist yet yield no engines (e.g. every engine disabled) —
	// mirroring query.py L204-212, where an existing-but-empty category
	// still makes the bang "specific" (returns true) without appending
	// any EngineRef.
	EnginesInCategory(category string) ([]string, bool)
}

// RegistryCatalog adapts *engine.Registry plus injected lookup tables to
// EngineCatalog (TASK-005 decision). The shortcut and category maps are not
// derivable from the Registry (module factories carry no instance metadata),
// so TASK-006 must build them from the configured engine instances before
// wiring the parser. Engines, when provided, adds instance names that are
// not also module names (e.g. "duckduckgo_extra").
type RegistryCatalog struct {
	Reg        *engine.Registry
	Shortcuts  map[string]string   // shortcut -> engine name (lowercase keys)
	Engines    map[string]bool     // known engine instance names (lowercase)
	Categories map[string][]string // category -> enabled engine names (lowercase)
}

// Has reports whether name is a registered module or a known instance name.
func (c *RegistryCatalog) Has(name string) bool {
	if c.Reg != nil && c.Reg.Has(name) {
		return true
	}
	return c.Engines[name]
}

// ResolveShortcut looks the shortcut up in the injected shortcut map.
func (c *RegistryCatalog) ResolveShortcut(shortcut string) (string, bool) {
	name, ok := c.Shortcuts[shortcut]
	return name, ok
}

// EnginesInCategory returns the injected engines of a category.
func (c *RegistryCatalog) EnginesInCategory(category string) ([]string, bool) {
	engines, ok := c.Categories[category]
	return engines, ok
}

// Parse parses the raw query into a RawTextQuery (TASK-005), mirroring the
// SearXNG RawTextQuery constructor + _parse_query (query.py L261-309).
//
// store is consumed through the bang.BangStore interface (never the
// concrete *bang.Store, per the TASK-005 <-> TASK-017 decoupling), catalog
// supplies engines/shortcuts/categories. A nil store or nil catalog is
// tolerated: the corresponding lookups simply never match, so such tokens
// fall back to the plain user query.
func Parse(raw string, store bang.BangStore, catalog EngineCatalog) *RawTextQuery {
	rtq := &RawTextQuery{Query: raw}
	parseQuery(rtq, store, catalog)
	return rtq
}

// parserClass mirrors one entry of RawTextQuery.PARSER_CLASSES
// (query.py L253-259). check decides whether the parser claims a token;
// call performs the actual parse and reports whether the token is a
// special query part. The ordered parserClasses list below must keep this
// exact order: ExternalBangParser must precede BangParser so "!!name" is
// never seen as a bang.
type parserClass struct {
	check func(token string) bool
	call  func(rtq *RawTextQuery, store bang.BangStore, catalog EngineCatalog, token string) bool
}

// parserClasses is the fixed parser pipeline (query.py PARSER_CLASSES,
// L253-259): TimeoutParser, LanguageParser, ExternalBangParser,
// BangParser, FeelingLuckyParser.
var parserClasses = []parserClass{
	{check: hasPrefix("<"), call: callTimeoutParser},
	{check: hasPrefix(":"), call: callLanguageParser},
	{check: func(t string) bool { return strings.HasPrefix(t, "!!") && len(t) > 2 }, call: callExternalBangParser},
	{check: func(t string) bool { return strings.HasPrefix(t, "!") && (len(t) < 2 || t[1] != '!') }, call: callBangParser},
	{check: func(t string) bool { return t == "!!" }, call: callFeelingLuckyParser},
}

// hasPrefix builds a check helper for the single-character parsers.
func hasPrefix(prefix string) func(string) bool {
	return func(t string) bool { return strings.HasPrefix(t, prefix) }
}

// parseQuery is the port of _parse_query (query.py L280-309). The query is
// split on whitespace (strings.Fields matches re.split(r'(\s+)') semantics
// after dropping whitespace-only parts, query.py L287-295; the user query
// is rebuilt with strings.Join). For every token the first matching parser
// claims it: special parts go to QueryParts, everything else to
// UserQueryParts.
func parseQuery(rtq *RawTextQuery, store bang.BangStore, catalog EngineCatalog) {
	for _, token := range strings.Fields(rtq.Query) {
		special := false
		for _, pc := range parserClasses {
			if pc.check(token) {
				// First matching check wins; the call decides whether the
				// token is a special part (query.py L299-302).
				special = pc.call(rtq, store, catalog, token)
				break
			}
		}
		if special {
			rtq.QueryParts = append(rtq.QueryParts, token)
		} else {
			rtq.UserQueryParts = append(rtq.UserQueryParts, token)
		}
	}
}

// isAllDigits reports whether s consists exclusively of ASCII digits. It
// mirrors Python's str.isdigit() for the ASCII token case used here and
// intentionally excludes the +/- signs strconv.Atoi would also accept.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// callTimeoutParser implements TimeoutParser (query.py L43-65). A token
// "<N": below 100 the unit is seconds, 100 and above it is milliseconds.
func callTimeoutParser(rtq *RawTextQuery, _ bang.BangStore, _ EngineCatalog, token string) bool {
	value := token[1:]
	// __call__ (L48-53): empty value is not special.
	if value == "" {
		return false
	}
	// _parse (L55-65)
	if !isAllDigits(value) {
		return false
	}
	n, _ := strconv.Atoi(value)
	if n < 100 {
		// below 100, the unit is the second ( <3 = 3 seconds timeout )
		rtq.TimeoutLimit = float64Ptr(float64(n))
	} else {
		// 100 or above, the unit is the millisecond ( <850 = 850 milliseconds timeout )
		rtq.TimeoutLimit = float64Ptr(float64(n) / 1000.0)
	}
	return true
}

// validLanguageCode mirrors SearXNG's VALID_LANGUAGE_CODE
// (searx/webutils.py L32): 2-3 lowercase letters with an optional
// "-xx" region suffix.
var validLanguageCode = regexp.MustCompile(`^[a-z]{2,3}(-[a-zA-Z]{2})?$`)

// callLanguageParser implements LanguageParser (query.py L72-116). The full
// sxng_locales table is out of scope for TASK-005: only the "valid yet not
// selectable language" fallback branch (L107-114) is ported, so a token
// ":xx" is special when xx is "auto" or matches validLanguageCode.
func callLanguageParser(rtq *RawTextQuery, _ bang.BangStore, _ EngineCatalog, token string) bool {
	// __call__ (L77-82): lowercase, normalize '_' to '-'.
	value := strings.ToLower(token[1:])
	value = strings.ReplaceAll(value, "_", "-")
	if value == "" {
		return false
	}
	return parseLanguage(rtq, value)
}

// parseLanguage implements the fallback branch of LanguageParser._parse
// (query.py L107-114).
func parseLanguage(rtq *RawTextQuery, value string) bool {
	if value != "auto" && !validLanguageCode.MatchString(value) {
		return false
	}
	// L109-111: "en-us" -> "en-US", "fr-fr" -> "fr-FR".
	if parts := strings.Split(value, "-"); len(parts) > 1 {
		value = strings.ToLower(parts[0]) + "-" + strings.ToUpper(parts[1])
	}
	// L112-114: dedupe; a repeated language is not special and falls back
	// to the plain user query (faithful port, query.py semantics).
	for _, lang := range rtq.Languages {
		if lang == value {
			return false
		}
	}
	rtq.Languages = append(rtq.Languages, value)
	return true
}

// callExternalBangParser implements ExternalBangParser (query.py L151-169).
// A token "!!name" resolves against the BangStore: on a hit the bang name
// (lowercased, without the "!!" prefix) is stored in ExternalBang.
func callExternalBangParser(rtq *RawTextQuery, store bang.BangStore, _ EngineCatalog, token string) bool {
	// __call__ (L156-161): value is the lowercased bang name.
	value := strings.ToLower(token[2:])
	if value == "" || store == nil {
		return false
	}
	// _parse (L163-169)
	if _, ok := store.Lookup(value); ok {
		rtq.ExternalBang = value
		return true
	}
	return false
}

// callBangParser implements BangParser (query.py L178-214): "!x" resolves
// in order as an engine shortcut, an engine name or a category. A
// successful resolution marks the query as Specific.
func callBangParser(rtq *RawTextQuery, _ bang.BangStore, catalog EngineCatalog, token string) bool {
	if catalog == nil {
		return false
	}
	// __call__ (L184-191): normalize '-' and '_' to spaces, lowercase.
	value := token[1:]
	value = strings.ReplaceAll(value, "-", " ")
	value = strings.ReplaceAll(value, "_", " ")
	value = strings.ToLower(value)
	if value == "" {
		return false
	}
	// _parse (L193-214)
	// 1. engine shortcut (L195-196)
	if name, ok := catalog.ResolveShortcut(value); ok {
		value = name
	}
	// 2. engine name (L199-201)
	if catalog.Has(value) {
		rtq.Enginerefs = append(rtq.Enginerefs, EngineRef{Name: value, Category: "none"})
		rtq.Specific = true
		return true
	}
	// 3. category (L204-212): expand to one EngineRef per enabled engine.
	if engines, ok := catalog.EnginesInCategory(value); ok {
		for _, name := range engines {
			rtq.Enginerefs = append(rtq.Enginerefs, EngineRef{Name: name, Category: value})
		}
		rtq.Specific = true
		return true
	}
	return false
}

// callFeelingLuckyParser implements FeelingLuckyParser (query.py L240-247):
// the bare "!!" token redirects to the first result.
func callFeelingLuckyParser(rtq *RawTextQuery, _ bang.BangStore, _ EngineCatalog, token string) bool {
	rtq.RedirectToFirstResult = true
	return true
}

// float64Ptr returns a pointer to v, for optional float fields.
func float64Ptr(v float64) *float64 {
	return &v
}
