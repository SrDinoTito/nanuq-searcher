package search

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	nanuq "nanuq-engine/pkg/nanuq"

	"nanuq-searcher-mcp/internal/domain"
)

// newEmptySvc builds a nanuq.Service from a minimal temp settings file with
// an empty engine catalog. With no engines the catalog has no "all" category,
// so Search performs zero requests: fully offline.
func newEmptySvc(t *testing.T) *nanuq.Service {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.yml")
	if err := os.WriteFile(path, []byte("engines: []\n"), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	svc, err := nanuq.NewServiceFromFile(path, nil)
	if err != nil {
		t.Fatalf("NewServiceFromFile: %v", err)
	}
	return svc
}

func TestAdapterSearchNormalEmptyResults(t *testing.T) {
	adapter := NewAdapter(newEmptySvc(t))
	res, err := adapter.Search(context.Background(), "golang", nil, 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res == nil {
		t.Fatal("Search returned nil result")
	}
	if res.Query != "golang" {
		t.Errorf("Query = %q, want %q", res.Query, "golang")
	}
	if res.Hits == nil {
		t.Error("Hits is nil, want non-nil empty slice")
	}
	if len(res.Hits) != 0 {
		t.Errorf("len(Hits) = %d, want 0", len(res.Hits))
	}
}

// TestAdapterSearchContractNeverNilHits is the DSG-014 contract test backing
// REQ-007: Service.Search must always return a *SearchResult with a non-nil
// Hits slice — even for a fully offline catalog (engines: []), bang
// redirects, empty queries and empty results — so the markdown renderer can
// range over Hits without a nil-slice guard.
func TestAdapterSearchContractNeverNilHits(t *testing.T) {
	adapter := NewAdapter(newEmptySvc(t))
	for _, query := range []string{"golang", "hola", "!!ddg golang", ""} {
		t.Run("query="+query, func(t *testing.T) {
			res, err := adapter.Search(context.Background(), query, nil, 10)
			if err != nil {
				t.Fatalf("Search(%q): %v", query, err)
			}
			if res == nil {
				t.Fatalf("Search(%q) returned nil result", query)
			}
			if res.Hits == nil {
				t.Errorf("Search(%q): Hits is nil — renderer contract violated", query)
			}
		})
	}
}

func TestAdapterSearchEmptyQuery(t *testing.T) {
	adapter := NewAdapter(newEmptySvc(t))
	res, err := adapter.Search(context.Background(), "", nil, 10)
	if err != nil {
		t.Fatalf("Search empty query: %v", err)
	}
	if res == nil {
		t.Fatal("Search returned nil result")
	}
	if len(res.Hits) != 0 {
		t.Errorf("len(Hits) = %d, want 0", len(res.Hits))
	}
}

func TestAdapterSearchDefaultMaxResults(t *testing.T) {
	// maxResults <= 0 must fall back to the default without error (REQ-002).
	adapter := NewAdapter(newEmptySvc(t))
	if _, err := adapter.Search(context.Background(), "hola", nil, 0); err != nil {
		t.Fatalf("Search with maxResults=0: %v", err)
	}
	if _, err := adapter.Search(context.Background(), "hola", nil, -5); err != nil {
		t.Fatalf("Search with maxResults=-5: %v", err)
	}
}

func TestAdapterSearchExternalBangRedirect(t *testing.T) {
	// "!!ddg hola" resolves to an external bang via the embedded bang store;
	// the redirect short-circuits standard search, so this is offline.
	adapter := NewAdapter(newEmptySvc(t))
	res, err := adapter.Search(context.Background(), "!!ddg hola", nil, 10)
	if err != nil {
		t.Fatalf("Search bang: %v", err)
	}
	if res.RedirectURL == "" {
		t.Error("RedirectURL is empty, want external bang URL")
	}
	if !strings.Contains(res.RedirectURL, "duckduckgo.com") {
		t.Errorf("RedirectURL = %q, want duckduckgo URL", res.RedirectURL)
	}
}

func TestAdapterSearchCategoriesSupported(t *testing.T) {
	// The engine supports !category bang syntax, so categories are injected
	// as a prefix. With an empty catalog they degrade to plain words and the
	// search still succeeds with zero hits.
	adapter := NewAdapter(newEmptySvc(t))
	res, err := adapter.Search(context.Background(), "hola", []string{"general"}, 10)
	if err != nil {
		t.Fatalf("Search with categories: %v", err)
	}
	if res == nil || len(res.Hits) != 0 {
		t.Errorf("res = %+v, want zero hits", res)
	}
}

func TestAdapterNilService(t *testing.T) {
	if _, err := NewAdapter(nil).Search(context.Background(), "hola", nil, 10); err == nil {
		t.Error("Search with nil service: want error, got nil")
	}
}

func TestAdapterContextCanceled(t *testing.T) {
	adapter := NewAdapter(newEmptySvc(t))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := adapter.Search(ctx, "hola", nil, 10); err == nil {
		t.Error("Search with canceled ctx: want error, got nil")
	}
}

func TestTruncateHits(t *testing.T) {
	hits := func(n int) []domain.SearchHit {
		out := make([]domain.SearchHit, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, domain.SearchHit{Title: string(rune('a' + i))})
		}
		return out
	}

	t.Run("caps at max", func(t *testing.T) {
		got := truncateHits(hits(5), 3)
		if len(got) != 3 {
			t.Fatalf("len = %d, want 3", len(got))
		}
		if got[0].Title != "a" || got[2].Title != "c" {
			t.Errorf("order not preserved: %+v", got)
		}
	})
	t.Run("max greater than len keeps all", func(t *testing.T) {
		got := truncateHits(hits(3), 10)
		if len(got) != 3 {
			t.Errorf("len = %d, want 3", len(got))
		}
	})
	t.Run("max zero keeps all", func(t *testing.T) {
		got := truncateHits(hits(3), 0)
		if len(got) != 3 {
			t.Errorf("len = %d, want 3", len(got))
		}
	})
	t.Run("max negative keeps all", func(t *testing.T) {
		got := truncateHits(hits(3), -1)
		if len(got) != 3 {
			t.Errorf("len = %d, want 3", len(got))
		}
	})
	t.Run("nil hits", func(t *testing.T) {
		if got := truncateHits(nil, 2); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
}

func TestMapResult(t *testing.T) {
	raw := []map[string]any{{"title": "t", "content": "c", "url": "u"}}
	hits := ProjectMany(raw)

	res := &nanuq.Result{
		Query:        "q",
		Results:      raw,
		Unresponsive: []string{"engine-a", "engine-b"},
		RedirectURL:  "http://duckduckgo.com/?q=hola",
	}
	got := mapResult(res, hits)
	if got == nil {
		t.Fatal("mapResult returned nil")
	}
	if got.Query != "q" {
		t.Errorf("Query = %q, want %q", got.Query, "q")
	}
	if len(got.Hits) != 1 || got.Hits[0].Title != "t" {
		t.Errorf("Hits = %+v, want projected 1 hit with title t", got.Hits)
	}
	if len(got.Unresponsive) != 2 || got.Unresponsive[0] != "engine-a" {
		t.Errorf("Unresponsive = %v, want [engine-a engine-b]", got.Unresponsive)
	}
	if got.RedirectURL != "http://duckduckgo.com/?q=hola" {
		t.Errorf("RedirectURL = %q, want duckduckgo URL", got.RedirectURL)
	}
}

func TestMapResultNil(t *testing.T) {
	got := mapResult(nil, nil)
	if got == nil {
		t.Fatal("mapResult(nil, nil) returned nil")
	}
	if got.Query != "" || got.Hits != nil || got.RedirectURL != "" {
		t.Errorf("mapResult(nil) = %+v, want zero value with nil hits", got)
	}
}

func TestWithCategoryPrefix(t *testing.T) {
	cases := []struct {
		name       string
		query      string
		categories []string
		want       string
	}{
		{"nil categories", "hola", nil, "hola"},
		{"empty slice", "hola", []string{}, "hola"},
		{"single", "hola", []string{"general"}, "!general hola"},
		{"multiple", "hola", []string{"general", "news"}, "!general !news hola"},
		{"skips empty", "hola", []string{"general", "", "news", ""}, "!general !news hola"},
		{"empty query", "", []string{"general"}, "!general "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := withCategoryPrefix(tc.query, tc.categories); got != tc.want {
				t.Errorf("withCategoryPrefix(%q, %v) = %q, want %q", tc.query, tc.categories, got, tc.want)
			}
		})
	}
}
