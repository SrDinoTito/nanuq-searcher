package bang

import (
	"os"
	"strings"
	"testing"
)

// loadFixture loads the small trie fixture replicating the real SearXNG
// dataset format (testdata/bangs_fixture.json).
func loadFixture(t *testing.T) *Store {
	t.Helper()
	data, err := os.ReadFile("testdata/bangs_fixture.json")
	if err != nil {
		t.Fatalf("bang: read fixture: %v", err)
	}
	s := New()
	if err := s.Load(data); err != nil {
		t.Fatalf("bang: load fixture: %v", err)
	}
	return s
}

func TestStoreInterface(t *testing.T) {
	// The query parser (TASK-005) consumes BangStore; ensure *Store satisfies it.
	var _ BangStore = (*Store)(nil)
}

func TestLoadFixtureLen(t *testing.T) {
	s := loadFixture(t)
	// Expected bangs: ddg, ddgi, .net, g, gol, wikipedia.
	if got := s.Len(); got != 6 {
		t.Fatalf("Len() = %d, want 6", got)
	}
}

func TestLookup(t *testing.T) {
	s := loadFixture(t)

	tests := []struct {
		name     string
		wantURL  string
		wantRank int
		wantOK   bool
	}{
		{name: "ddg", wantURL: "http://duckduckgo.com/?q=\x02", wantRank: 19, wantOK: true},
		{name: "ddgi", wantURL: "//duckduckgo.com/?q=\x02&iax=images&ia=images", wantRank: 762, wantOK: true},
		{name: ".net", wantURL: "http://www.searchdotnet.com/results.aspx?q=\x02", wantRank: 5, wantOK: true},
		{name: "g", wantURL: "//google.com/search?q=\x02", wantRank: 0, wantOK: true},
		{name: "gol", wantURL: "//golang.org/search?q=\x02", wantRank: 0, wantOK: true},
		{name: "wikipedia", wantURL: "//en.wikipedia.org/w/index.php?search=\x02", wantRank: 1, wantOK: true},
		{name: "unknown-bang", wantOK: false},
		{name: "!ddg", wantOK: false}, // names are stored without the "!" prefix
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def, ok := s.Lookup(tt.name)
			if ok != tt.wantOK {
				t.Fatalf("Lookup(%q) ok = %v, want %v", tt.name, ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if def.URL != tt.wantURL {
				t.Errorf("Lookup(%q).URL = %q, want %q", tt.name, def.URL, tt.wantURL)
			}
			if def.Rank != tt.wantRank {
				t.Errorf("Lookup(%q).Rank = %d, want %d", tt.name, def.Rank, tt.wantRank)
			}
		})
	}
}

func TestGetBangURL(t *testing.T) {
	s := loadFixture(t)

	tests := []struct {
		name  string
		query string
		want  string
	}{
		// URL-escaped query interpolation (CA-008: "!!ddg hola").
		{name: "ddg", query: "hola", want: "http://duckduckgo.com/?q=hola"},
		{name: "ddg", query: "hola mundo", want: "http://duckduckgo.com/?q=hola+mundo"},
		{name: "ddgi", query: "a&b=c", want: "https://duckduckgo.com/?q=a%26b%3Dc&iax=images&ia=images"},
		// "//" prefix resolves to "https:".
		{name: "g", query: "test", want: "https://google.com/search?q=test"},
		{name: "gol", query: "go", want: "https://golang.org/search?q=go"},
		{name: "wikipedia", query: "dino", want: "https://en.wikipedia.org/w/index.php?search=dino"},
		{name: ".net", query: "cats", want: "http://www.searchdotnet.com/results.aspx?q=cats"},
		// Empty query falls back to the main page (scheme://netloc).
		{name: "g", query: "", want: "https://google.com"},
		{name: "ddg", query: "", want: "http://duckduckgo.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name+"/"+tt.query, func(t *testing.T) {
			got, ok := s.GetBangURL(tt.name, tt.query)
			if !ok {
				t.Fatalf("GetBangURL(%q, %q) ok = false, want true", tt.name, tt.query)
			}
			if got != tt.want {
				t.Errorf("GetBangURL(%q, %q) = %q, want %q", tt.name, tt.query, got, tt.want)
			}
		})
	}

	if _, ok := s.GetBangURL("unknown-bang", "x"); ok {
		t.Error("GetBangURL(unknown-bang, x) ok = true, want false")
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		wantErr string
	}{
		{name: "invalid json", data: "not json", wantErr: "parse dataset"},
		{name: "missing trie", data: `{}`, wantErr: "missing \"trie\" key"},
		{name: "leaf without separator", data: `{"trie": {"x": "http://example.com"}}`, wantErr: "got 1 fields"},
		{name: "invalid rank", data: `{"trie": {"x": "http://e.com/?q=\u0002\u0001abc"}}`, wantErr: "invalid rank"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New()
			err := s.Load([]byte(tt.data))
			if err == nil {
				t.Fatal("Load() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Load() error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadReload(t *testing.T) {
	// Load replaces previous definitions.
	s := New()
	if err := s.Load([]byte(`{"trie": {"a": "http://a.example/?q=\u0002\u00010"}}`)); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := s.Load([]byte(`{"trie": {"b": "http://b.example/?q=\u0002\u00010"}}`)); err != nil {
		t.Fatalf("Load (second): %v", err)
	}
	if s.Len() != 1 {
		t.Fatalf("Len() = %d, want 1 after reload", s.Len())
	}
	if _, ok := s.Lookup("a"); ok {
		t.Error("Lookup(a) ok = true after reload, want false")
	}
	if _, ok := s.Lookup("b"); !ok {
		t.Error("Lookup(b) ok = false, want true")
	}
}

func TestLoadEmbeddedRealDataset(t *testing.T) {
	// End-to-end check against the bundled real dataset (19,076-line
	// external_bangs.json embedded into the test binary via go:embed).
	s := New()
	if err := s.LoadEmbedded(); err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}
	// The bundled dataset currently holds 13,561 bangs (10,806 leaf strings
	// + 2,755 dict nodes with a LEAF_KEY entry — independently verified).
	// The 19k figure in the spec refers to the raw file's line count
	// (19,076 lines), not the number of bang definitions. Use a floor so
	// the test survives a legitimate dataset update.
	if got := s.Len(); got < 10000 {
		t.Fatalf("Len() = %d, want >= 10000 for the real dataset", got)
	}

	// CA-008: "!!ddg hola" must resolve to the DuckDuckGo URL with "hola"
	// URL-encoded.
	url, ok := s.GetBangURL("ddg", "hola")
	if !ok {
		t.Fatal("GetBangURL(ddg, hola) ok = false, want true")
	}
	if want := "http://duckduckgo.com/?q=hola"; url != want {
		t.Errorf("GetBangURL(ddg, hola) = %q, want %q", url, want)
	}

	// A nested bang reached through prefix compression (d.d.g.i).
	if def, ok := s.Lookup("ddgi"); !ok {
		t.Error("Lookup(ddgi) ok = false, want true")
	} else if want := "//duckduckgo.com/?q=\x02&iax=images&ia=images"; def.URL != want {
		t.Errorf("Lookup(ddgi).URL = %q, want %q", def.URL, want)
	}

	// Bang names are stored without the "!" prefix.
	if _, ok := s.Lookup("!ddg"); ok {
		t.Error("Lookup(!ddg) ok = true, want false")
	}
	// Unknown bang.
	if _, ok := s.Lookup("nonexistentbangxyz"); ok {
		t.Error("Lookup(nonexistentbangxyz) ok = true, want false")
	}
}
