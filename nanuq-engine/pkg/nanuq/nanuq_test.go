package nanuq

import (
	"errors"
	"testing"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
)

// TestNewFactory verifies that NewFactory registers every module
// (2 data-driven + 12 core = 14) and that List reflects them.
func TestNewFactory(t *testing.T) {
	f, err := NewFactory()
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}
	infos := f.List()
	if len(infos) != 14 {
		t.Fatalf("List() has %d modules, want 14: %v", len(infos), infos)
	}
	want := map[string]bool{
		"xpath": true, "json_engine": true,
		"wikipedia": true, "baidu": true, "duckduckgo": true, "bing": true,
		"bing_images": true, "bing_news": true, "bing_videos": true,
		"google": true, "brave": true, "qwant": true, "mojeek": true,
		"startpage": true,
	}
	seen := make(map[string]bool, len(infos))
	for _, info := range infos {
		seen[info.Kind] = true
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("module %q not registered", name)
		}
	}
}

// TestListSorted verifies List() returns modules in ascending order.
func TestListSorted(t *testing.T) {
	f, err := NewFactory()
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}
	infos := f.List()
	for i := 1; i < len(infos); i++ {
		if infos[i-1].Kind >= infos[i].Kind {
			t.Errorf("List() not sorted at %d: %q >= %q", i, infos[i-1].Kind, infos[i].Kind)
		}
	}
}

// TestInstantiateXPath verifies that the xpath module requires its
// mandatory overrides (search_url + results_xpath) and that a valid
// config yields the public Engine metadata.
func TestInstantiateXPath(t *testing.T) {
	f, err := NewFactory()
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}

	// Missing overrides -> ErrInvalidConfig.
	if _, err := f.Instantiate(config.EngineConfig{Name: "xp", Engine: "xpath"}); !errors.Is(err, engine.ErrInvalidConfig) {
		t.Fatalf("Instantiate(xpath, no overrides) error = %v, want wrap of engine.ErrInvalidConfig", err)
	}

	// Valid overrides -> Engine metadata.
	eng, err := f.Instantiate(config.EngineConfig{
		Name:       "xp_ok",
		Engine:     "xpath",
		Shortcut:   "xp",
		Categories: []string{"general"},
		Overrides: map[string]any{
			"search_url":    "https://example.org/search?q={query}",
			"results_xpath": "//div[contains(@class, 'result')]",
		},
	})
	if err != nil {
		t.Fatalf("Instantiate(xpath, ok) error = %v", err)
	}
	if eng.Kind != "xpath" {
		t.Errorf("Kind = %q, want %q", eng.Kind, "xpath")
	}
	if eng.Name != "xp_ok" {
		t.Errorf("Name = %q, want %q", eng.Name, "xp_ok")
	}
	if len(eng.Categories) != 1 || eng.Categories[0] != "general" {
		t.Errorf("Categories = %v, want [general]", eng.Categories)
	}
}

// TestInstantiateUnknown verifies that an unregistered module surfaces
// engine.ErrEngineNotFound (wrapped with %w).
func TestInstantiateUnknown(t *testing.T) {
	f, err := NewFactory()
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}
	_, err = f.Instantiate(config.EngineConfig{Name: "u", Engine: "no_such_module"})
	if !errors.Is(err, engine.ErrEngineNotFound) {
		t.Fatalf("Instantiate(unknown) error = %v, want wrap of engine.ErrEngineNotFound", err)
	}
}

// TestNewService verifies the full pipeline wiring: bang dataset loaded
// (13,561 entries) and a nil-safe result for an empty query.
func TestNewService(t *testing.T) {
	f, err := NewFactory()
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}
	cfg := &config.Config{Search: config.Search{SafeSearch: 0}, Outgoing: config.Outgoing{}}
	svc, err := NewService(cfg, f.reg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if n := svc.bang.Len(); n != 13561 {
		t.Errorf("bang store Len() = %d, want 13561", n)
	}
}

// TestServiceSearchEmpty verifies an empty query does not panic and
// yields an empty, non-nil Result without any network I/O.
func TestServiceSearchEmpty(t *testing.T) {
	f, err := NewFactory()
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}
	cfg := &config.Config{Search: config.Search{SafeSearch: 0}, Outgoing: config.Outgoing{}}
	svc, err := NewService(cfg, f.reg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	res, err := svc.Search("")
	if err != nil {
		t.Fatalf("Search(\"\") error = %v", err)
	}
	if res == nil {
		t.Fatal("Search(\"\") returned nil Result")
	}
	if len(res.Results) != 0 {
		t.Errorf("Search(\"\") Results = %d entries, want 0", len(res.Results))
	}
}

// TestServiceBang verifies external-bang resolution through the public
// facade: "!!ddg hola" resolves to the DuckDuckGo redirect URL.
func TestServiceBang(t *testing.T) {
	f, err := NewFactory()
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}
	cfg := &config.Config{Search: config.Search{SafeSearch: 0}, Outgoing: config.Outgoing{}}
	svc, err := NewService(cfg, f.reg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	url, ok := svc.Bangs("!!ddg hola")
	if !ok {
		t.Fatal("Bangs(\"!!ddg hola\") ok = false, want true")
	}
	if url != "http://duckduckgo.com/?q=hola" {
		t.Errorf("Bangs url = %q, want %q", url, "http://duckduckgo.com/?q=hola")
	}

	// A plain query carries no external bang.
	if _, ok := svc.Bangs("hola mundo"); ok {
		t.Error("Bangs(\"hola mundo\") ok = true, want false")
	}
}
