package search

import (
	"testing"

	"nanuq-engine/internal/bang"
	"nanuq-engine/internal/engine"
)

// fakeBangStore is a minimal BangStore for tests (the concrete *bang.Store
// is not used here).
type fakeBangStore struct {
	bangs map[string]bang.BangDef
}

func (f *fakeBangStore) Lookup(name string) (bang.BangDef, bool) {
	def, ok := f.bangs[name]
	return def, ok
}

// fakeCatalog is a minimal EngineCatalog for tests.
type fakeCatalog struct {
	engines    map[string]bool
	shortcuts  map[string]string
	categories map[string][]string
}

func (f *fakeCatalog) Has(name string) bool { return f.engines[name] }
func (f *fakeCatalog) ResolveShortcut(s string) (string, bool) {
	name, ok := f.shortcuts[s]
	return name, ok
}
func (f *fakeCatalog) EnginesInCategory(c string) ([]string, bool) {
	engines, ok := f.categories[c]
	return engines, ok
}

// fptr is a test helper returning a *float64.
func fptr(v float64) *float64 { return &v }

func TestParse(t *testing.T) {
	store := &fakeBangStore{bangs: map[string]bang.BangDef{
		"golang": {URL: "https://golang.org/?q=\x02", Rank: 1},
		"ddg":    {URL: "//duckduckgo.com/?q=\x02", Rank: 5},
	}}
	catalog := &fakeCatalog{
		engines:   map[string]bool{"ddg": true, "google": true},
		shortcuts: map[string]string{"g": "google"},
		categories: map[string][]string{
			"general": {"ddg", "google"},
			"empty":   {}, // category exists but yields no enabled engines
		},
	}

	tests := []struct {
		name string
		raw  string
		// inputs (nil uses the shared fake store/catalog above)
		store   bang.BangStore
		catalog EngineCatalog
		// expectations
		wantUserQuery  string
		wantQueryParts []string
		wantEnginerefs []EngineRef
		wantLanguages  []string
		wantTimeout    *float64
		wantExternal   string
		wantSpecific   bool
		wantRedirect   bool
	}{
		// --- spec table (TASK-005 criteria) ---
		{name: "engine bang", raw: "!ddg hola",
			wantEnginerefs: []EngineRef{{"ddg", "none"}}, wantSpecific: true, wantUserQuery: "hola"},
		{name: "external bang", raw: "!!golang algo",
			wantExternal: "golang", wantUserQuery: "algo"},
		{name: "language", raw: ":es consulta",
			wantLanguages: []string{"es"}, wantUserQuery: "consulta"},
		{name: "timeout seconds", raw: "<2 test",
			wantTimeout: fptr(2.0), wantUserQuery: "test"},
		{name: "feeling lucky", raw: "!! test",
			wantRedirect: true, wantUserQuery: "test"},
		{name: "category expansion", raw: "consulta !general",
			wantEnginerefs: []EngineRef{{"ddg", "general"}, {"google", "general"}},
			wantSpecific:   true, wantUserQuery: "consulta"},

		// --- faithful ports of query.py behavior ---
		{name: "timeout milliseconds", raw: "<850 test",
			wantTimeout: fptr(0.85), wantUserQuery: "test"},
		{name: "timeout invalid", raw: "<abc x",
			wantUserQuery: "<abc x"},
		{name: "timeout negative sign is not a timeout", raw: "<-2 x",
			wantUserQuery: "<-2 x"},
		{name: "bare timeout char is user text", raw: "< x",
			wantUserQuery: "< x"},
		{name: "language region normalizes case", raw: ":en-us hola",
			wantLanguages: []string{"en-US"}, wantUserQuery: "hola"},
		{name: "language underscore input", raw: ":fr_fr bonjour",
			wantLanguages: []string{"fr-FR"}, wantUserQuery: "bonjour"},
		{name: "language auto", raw: ":auto x",
			wantLanguages: []string{"auto"}, wantUserQuery: "x"},
		{name: "language invalid falls back to user text", raw: ":x consulta",
			wantUserQuery: ":x consulta"},
		// NOTE: ":xx" IS accepted (Languages=["xx"]): VALID_LANGUAGE_CODE
		// (webutils.py L32) matches any 2-3 letter code — "user may set a
		// valid, yet not selectable language" (query.py L107).
		{name: "any two-letter code is a valid language", raw: ":xx consulta",
			wantLanguages: []string{"xx"}, wantUserQuery: "consulta"},
		{name: "repeated language is not special", raw: ":es :es hola",
			wantLanguages: []string{"es"}, wantUserQuery: ":es hola", wantQueryParts: []string{":es"}},
		{name: "external bang unknown", raw: "!!zzz algo",
			wantUserQuery: "!!zzz algo"},
		{name: "external bang lowercases", raw: "!!GoLang algo",
			wantExternal: "golang", wantUserQuery: "algo"},
		{name: "engine bang unknown", raw: "!zzz algo",
			wantUserQuery: "!zzz algo"},
		{name: "shortcut resolution", raw: "!g x",
			wantEnginerefs: []EngineRef{{"google", "none"}}, wantSpecific: true, wantUserQuery: "x"},
		{name: "bare bang char is user text", raw: "! test",
			wantUserQuery: "! test"},
		{name: "empty category stays specific", raw: "x !empty",
			wantSpecific: true, wantUserQuery: "x"},
		{name: "triple bang is not a bang", raw: "!!! x",
			wantUserQuery: "!!! x"},
		{name: "mixed special parts", raw: ":es !ddg <2 hola !!golang",
			wantEnginerefs: []EngineRef{{"ddg", "none"}}, wantLanguages: []string{"es"},
			wantTimeout: fptr(2.0), wantExternal: "golang", wantSpecific: true, wantUserQuery: "hola"},
		{name: "empty query", raw: ""},
		{name: "whitespace only query", raw: "   \t  "},
		{name: "nil store and nil catalog degrade safely", raw: "!ddg :es <2 !!golang hola",
			store: nil, catalog: nil,
			wantLanguages: []string{"es"}, wantTimeout: fptr(2.0), wantUserQuery: "!ddg !!golang hola"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Every case uses the shared fakes unless it explicitly
			// provides nil inputs (the nil-degradation case).
			var useStore bang.BangStore = store
			var useCatalog EngineCatalog = catalog
			if tt.store != nil || tt.name == "nil store and nil catalog degrade safely" {
				useStore = tt.store
			}
			if tt.catalog != nil || tt.name == "nil store and nil catalog degrade safely" {
				useCatalog = tt.catalog
			}

			got := Parse(tt.raw, useStore, useCatalog)

			if got.GetQuery() != tt.wantUserQuery {
				t.Errorf("GetQuery() = %q, want %q", got.GetQuery(), tt.wantUserQuery)
			}
			if tt.wantQueryParts != nil && !eqStrings(got.QueryParts, tt.wantQueryParts) {
				t.Errorf("QueryParts = %v, want %v", got.QueryParts, tt.wantQueryParts)
			}
			if !eqEngineRefs(got.Enginerefs, tt.wantEnginerefs) {
				t.Errorf("Enginerefs = %v, want %v", got.Enginerefs, tt.wantEnginerefs)
			}
			if !eqStrings(got.Languages, tt.wantLanguages) {
				t.Errorf("Languages = %v, want %v", got.Languages, tt.wantLanguages)
			}
			if !eqFloatPtr(got.TimeoutLimit, tt.wantTimeout) {
				t.Errorf("TimeoutLimit = %v, want %v", got.TimeoutLimit, tt.wantTimeout)
			}
			if got.ExternalBang != tt.wantExternal {
				t.Errorf("ExternalBang = %q, want %q", got.ExternalBang, tt.wantExternal)
			}
			if got.Specific != tt.wantSpecific {
				t.Errorf("Specific = %v, want %v", got.Specific, tt.wantSpecific)
			}
			if got.RedirectToFirstResult != tt.wantRedirect {
				t.Errorf("RedirectToFirstResult = %v, want %v", got.RedirectToFirstResult, tt.wantRedirect)
			}
		})
	}
}

// TestGetFullQuery verifies the getFullQuery port (query.py L326-330):
// special parts first, then the user query.
func TestGetFullQuery(t *testing.T) {
	store := &fakeBangStore{}
	catalog := &fakeCatalog{engines: map[string]bool{"ddg": true}}
	got := Parse("!ddg :es hola", store, catalog)
	if q := got.GetFullQuery(); q != "!ddg :es hola" {
		t.Errorf("GetFullQuery() = %q, want %q", q, "!ddg :es hola")
	}
	if q := got.GetQuery(); q != "hola" {
		t.Errorf("GetQuery() = %q, want %q", q, "hola")
	}

	// No special parts: full query equals the user query.
	got = Parse("hola mundo", store, catalog)
	if q := got.GetFullQuery(); q != "hola mundo" {
		t.Errorf("GetFullQuery() = %q, want %q", q, "hola mundo")
	}
}

// TestEngineCatalogAdapter exercises RegistryCatalog over an empty
// *engine.Registry with injected lookup tables.
func TestEngineCatalogAdapter(t *testing.T) {
	cat := &RegistryCatalog{
		Reg:        engine.New(),
		Shortcuts:  map[string]string{"g": "google"},
		Engines:    map[string]bool{"google": true, "duckduckgo_extra": true},
		Categories: map[string][]string{"general": {"google"}},
	}
	if !cat.Has("google") {
		t.Error("Has(google) = false, want true")
	}
	if !cat.Has("duckduckgo_extra") {
		t.Error("Has(duckduckgo_extra) = false, want true (instance name)")
	}
	if cat.Has("zzz") {
		t.Error("Has(zzz) = true, want false")
	}
	if name, ok := cat.ResolveShortcut("g"); !ok || name != "google" {
		t.Errorf("ResolveShortcut(g) = %q, %v; want google, true", name, ok)
	}
	if _, ok := cat.ResolveShortcut("zzz"); ok {
		t.Error("ResolveShortcut(zzz) = ok, want false")
	}
	engines, ok := cat.EnginesInCategory("general")
	if !ok || !eqStrings(engines, []string{"google"}) {
		t.Errorf("EnginesInCategory(general) = %v, %v; want [google], true", engines, ok)
	}
	if _, ok := cat.EnginesInCategory("images"); ok {
		t.Error("EnginesInCategory(images) = ok, want false")
	}
}

// eqStrings compares two string slices ignoring order-independent semantics
// (order is significant for the parser, so plain equality is used).
func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// eqEngineRefs compares two EngineRef slices.
func eqEngineRefs(a, b []EngineRef) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// eqFloatPtr compares two *float64 by value, treating nil specially.
func eqFloatPtr(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
