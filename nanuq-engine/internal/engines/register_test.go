package engines

import (
	"strings"
	"testing"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
)

// coreModules are the 12 core engine modules registered by RegisterAll,
// alongside the 2 data-driven modules (xpath, json_engine).
var coreModules = []string{
	"wikipedia",
	"baidu",
	"duckduckgo",
	"bing",
	"bing_images",
	"bing_news",
	"bing_videos",
	"google",
	"brave",
	"qwant",
	"mojeek",
	"startpage",
}

func TestRegisterAll(t *testing.T) {
	reg := engine.New()
	if err := RegisterAll(reg); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}

	if !reg.Has("xpath") {
		t.Error("data-driven module \"xpath\" not registered")
	}
	if !reg.Has("json_engine") {
		t.Error("data-driven module \"json_engine\" not registered")
	}
	for _, name := range coreModules {
		if !reg.Has(name) {
			t.Errorf("core module %q not registered", name)
		}
	}

	names := reg.Names()
	// 2 data-driven + 12 core modules.
	want := len(coreModules) + 2
	if len(names) != want {
		t.Errorf("Names() has %d entries, want %d: %v", len(names), want, names)
	}
}

func TestRegisterAllDuplicate(t *testing.T) {
	reg := engine.New()
	if err := RegisterAll(reg); err != nil {
		t.Fatalf("first RegisterAll: %v", err)
	}
	if err := RegisterAll(reg); err == nil {
		t.Fatal("second RegisterAll: expected duplicate registration error, got nil")
	} else if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("second RegisterAll: unexpected error: %v", err)
	}
}

// TestRegisterAllInstantiate instantiates every registered module with a
// minimal config. The two data-driven modules require mandatory overrides
// (search_url + results_xpath / search_url + results_query); all 12 core
// modules build with Categories ["general"] and empty overrides.
func TestRegisterAllInstantiate(t *testing.T) {
	reg := engine.New()
	if err := RegisterAll(reg); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}

	requiredOverrides := map[string]map[string]any{
		"xpath": {
			"search_url":    "https://example.org/search?q={query}",
			"results_xpath": "//div[contains(@class, 'result')]",
		},
		"json_engine": {
			"search_url":    "https://example.org/api/search?q={query}",
			"results_query": "$.results",
		},
	}

	for _, name := range reg.Names() {
		cfg := config.EngineConfig{
			Name:       "test_" + name,
			Engine:     name,
			Categories: []string{"general"},
			Overrides:  requiredOverrides[name],
		}
		if cfg.Overrides == nil {
			cfg.Overrides = map[string]any{}
		}
		eng, err := reg.Instantiate(cfg)
		if err != nil {
			t.Errorf("Instantiate(%q): %v", name, err)
			continue
		}
		if eng == nil {
			t.Errorf("Instantiate(%q): returned nil engine", name)
		}
	}
}
