package i18n

import (
	"slices"
	"testing"
)

func TestNewLoadsLocales(t *testing.T) {
	b := New()
	if b == nil {
		t.Fatal("New() returned nil")
	}
	langs := b.AvailableLangs()
	if len(langs) != 2 {
		t.Fatalf("AvailableLangs() = %v, want exactly 2 locales", langs)
	}
	if !slices.Contains(langs, "en") || !slices.Contains(langs, "es") {
		t.Fatalf("AvailableLangs() = %v, want both en and es", langs)
	}
}

func TestLocalizeEnglish(t *testing.T) {
	b := New()

	if got := b.Localize("en", "search.submit", nil); got != "Search" {
		t.Errorf("search.submit en = %q, want %q", got, "Search")
	}
	if got := b.Localize("en", "search.placeholder", nil); got != "search the web..." {
		t.Errorf("search.placeholder en = %q, want %q", got, "search the web...")
	}
	if got := b.Localize("en", "results.for_query", map[string]any{"Query": "golang"}); got != "Results for golang" {
		t.Errorf("results.for_query en = %q, want %q", got, "Results for golang")
	}
	if got := b.Localize("en", "results.no_results", nil); got != "No results found." {
		t.Errorf("results.no_results en = %q, want %q", got, "No results found.")
	}
	if got := b.Localize("en", "error.too_many_requests", nil); got != "Too many requests" {
		t.Errorf("error.too_many_requests en = %q, want %q", got, "Too many requests")
	}
}

func TestLocalizeSpanish(t *testing.T) {
	b := New()

	if got := b.Localize("es", "search.submit", nil); got != "Buscar" {
		t.Errorf("search.submit es = %q, want %q", got, "Buscar")
	}
	if got := b.Localize("es", "search.placeholder", nil); got != "busca en la web..." {
		t.Errorf("search.placeholder es = %q, want %q", got, "busca en la web...")
	}
	if got := b.Localize("es", "results.for_query", map[string]any{"Query": "golang"}); got != "Resultados para golang" {
		t.Errorf("results.for_query es = %q, want %q", got, "Resultados para golang")
	}
	if got := b.Localize("es", "results.no_results", nil); got != "No se encontraron resultados." {
		t.Errorf("results.no_results es = %q, want %q", got, "No se encontraron resultados.")
	}
}

func TestLocalizePluralCount(t *testing.T) {
	b := New()

	enOne := b.Localize("en", "results.count", map[string]any{"Query": "x", "Count": 1})
	if enOne != "1 result for \"x\"" {
		t.Errorf("en singular = %q, want %q", enOne, "1 result for \"x\"")
	}
	enMany := b.Localize("en", "results.count", map[string]any{"Query": "x", "Count": 5})
	if enMany != "5 results for \"x\"" {
		t.Errorf("en plural = %q, want %q", enMany, "5 results for \"x\"")
	}

	esOne := b.Localize("es", "results.count", map[string]any{"Query": "x", "Count": 1})
	if esOne != "1 resultado para \"x\"" {
		t.Errorf("es singular = %q, want %q", esOne, "1 resultado para \"x\"")
	}
	esMany := b.Localize("es", "results.count", map[string]any{"Query": "x", "Count": 5})
	if esMany != "5 resultados para \"x\"" {
		t.Errorf("es plural = %q, want %q", esMany, "5 resultados para \"x\"")
	}
}

func TestUnknownKeyReturnsKey(t *testing.T) {
	b := New()

	if got := b.Localize("en", "no.such.key", nil); got != "no.such.key" {
		t.Errorf("unknown key = %q, want the key itself", got)
	}
}

func TestUnknownLangFallsBackToEnglish(t *testing.T) {
	b := New()

	if got := b.Localize("fr", "search.submit", nil); got != "Search" {
		t.Errorf("fr fallback = %q, want %q", got, "Search")
	}
}

func TestEmptyLangDefaultsToEnglish(t *testing.T) {
	b := New()

	if got := b.Localize("", "search.submit", nil); got != "Search" {
		t.Errorf("empty lang = %q, want %q", got, "Search")
	}
}
