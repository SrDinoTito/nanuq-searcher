// Package i18n provides a minimal localization bundle built on
// go-i18n/v2 (REQ-022, TASK-019). Locales are embedded at compile time
// via go:embed; English is the default fallback language.
//
// The public API is intentionally small:
//
//	b := i18n.New()
//	msg := b.Localize("es", "search.submit", nil)
//	langs := b.AvailableLangs()
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	g18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

//go:embed locales/*.json
var localeFS embed.FS

// Bundle wraps a go-i18n bundle together with the default language tag.
type Bundle struct {
	b           *g18n.Bundle
	defaultLang string
}

// New loads every embedded locale and returns a ready-to-use Bundle.
// It panics if an embedded locale cannot be read or parsed, matching the
// fail-fast convention used elsewhere in the codebase (template.Must).
func New() *Bundle {
	b := g18n.NewBundle(language.English)
	b.RegisterUnmarshalFunc("json", json.Unmarshal)

	entries, err := localeFS.ReadDir("locales")
	if err != nil {
		panic(fmt.Sprintf("i18n: read embedded locales: %v", err))
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if _, err := b.LoadMessageFileFS(localeFS, "locales/"+entry.Name()); err != nil {
			panic(fmt.Sprintf("i18n: load %s: %v", entry.Name(), err))
		}
	}

	return &Bundle{b: b, defaultLang: language.English.String()}
}

// Localize resolves key for the requested language tag (e.g. "en", "es",
// or an Accept-Language header). data is passed to the message template for
// interpolation (e.g. "Results for {{.Query}}"). If data contains a "Count"
// entry it is used to select the plural form.
//
// Resolution order:
//  1. key found in lang → translated message.
//  2. key missing in lang (or lang unknown/empty) → default language (en).
//  3. key missing everywhere → the key itself is returned.
func (b *Bundle) Localize(lang, key string, data map[string]any) string {
	if lang == "" {
		lang = b.defaultLang
	}

	msg := b.localize(lang, key, data)
	if msg != "" {
		return msg
	}

	// Explicit retry against the default language guarantees that an
	// unknown or malformed lang still falls back to en.
	if lang != b.defaultLang {
		if msg := b.localize(b.defaultLang, key, data); msg != "" {
			return msg
		}
	}

	return key
}

func (b *Bundle) localize(lang, key string, data map[string]any) string {
	l := g18n.NewLocalizer(b.b, lang)

	cfg := &g18n.LocalizeConfig{MessageID: key, TemplateData: data}
	// go-i18n does not detect the plural count from TemplateData; expose
	// it explicitly so plural keys select the right form.
	if count, ok := data["Count"]; ok {
		cfg.PluralCount = count
	}

	msg, err := l.Localize(cfg)
	if err == nil {
		return msg
	}
	// go-i18n returns a non-nil MessageNotFoundErr even when it already
	// fell back to the default language with a valid message — a non-empty
	// msg is therefore a successful fallback, not a miss.
	if msg != "" {
		return msg
	}
	return ""
}

// AvailableLangs returns the language tags of all loaded locales, sorted.
func (b *Bundle) AvailableLangs() []string {
	tags := b.b.LanguageTags()
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		out = append(out, t.String())
	}
	sort.Strings(out)
	return out
}
