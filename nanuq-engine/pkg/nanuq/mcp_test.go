package nanuq

import (
	"os"
	"path/filepath"
	"testing"
)

// writeSettings writes a minimal settings YAML to a temp file and returns
// its path. Tests live in the same package as the facade so they can
// inspect private fields (svc.cfg, f.reg) directly (TASK-017).
func writeSettings(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.yml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write settings %s: %v", path, err)
	}
	return path
}

// TestNewServiceFromFile builds a Service from a minimal settings YAML with
// two enabled engines and no API-key env vars. The Service must build
// cleanly, keep both engines enabled in its config, wire the bang store and
// answer Search without panicking (Result is never nil).
func TestNewServiceFromFile(t *testing.T) {
	path := writeSettings(t, `general:
  debug: false
outgoing:
  max_request_timeout: 2.0
engines:
  - name: wikipedia
    engine: wikipedia
    shortcut: wiki
    categories:
      - general
  - name: ddg
    engine: duckduckgo
    shortcut: ddg
    categories:
      - general
`)

	svc, err := NewServiceFromFile(path, nil)
	if err != nil {
		t.Fatalf("NewServiceFromFile: %v", err)
	}
	if svc == nil {
		t.Fatal("NewServiceFromFile returned nil Service")
	}

	// Both configured engines must be present and enabled. The cfg field is
	// private; the test reads it directly because it lives in package nanuq.
	if len(svc.cfg.Engines) != 2 {
		t.Fatalf("cfg.Engines has %d entries, want 2", len(svc.cfg.Engines))
	}
	for _, want := range []string{"wikipedia", "duckduckgo"} {
		found := false
		for i := range svc.cfg.Engines {
			if svc.cfg.Engines[i].Engine == want {
				found = true
				if svc.cfg.Engines[i].Disabled || svc.cfg.Engines[i].Inactive {
					t.Errorf("engine %q must be enabled", want)
				}
			}
		}
		if !found {
			t.Errorf("engine %q not configured", want)
		}
	}

	// Bang wiring must work end-to-end (external bang -> redirect URL),
	// proving the catalog was derived from cfg.Engines.
	url, ok := svc.Bangs("!!ddg hola")
	if !ok || url != "http://duckduckgo.com/?q=hola" {
		t.Errorf("Bangs(!!ddg hola) = %q, %v; want ddg URL, true", url, ok)
	}

	// Search must never panic and must return a non-nil Result. With real
	// engines the query goes to the network; max_request_timeout bounds it,
	// and without connectivity the engines are reported unresponsive and
	// Results stays empty — the contract is only that Result is non-nil.
	res, err := svc.Search("hola")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res == nil {
		t.Fatal("Search returned nil Result")
	}
	if res.Query != "hola" {
		t.Errorf("res.Query = %q, want %q", res.Query, "hola")
	}
}

// TestNewServiceFromFileEnvKeys verifies the D-04 API-key injection: the
// value of NANUQ_<ENGINE>_API_KEY (read via getenv = os.Getenv) must land
// in the brave engine's Overrides["api_key"], while an engine without a key
// requirement stays untouched.
func TestNewServiceFromFileEnvKeys(t *testing.T) {
	t.Setenv("NANUQ_BRAVE_API_KEY", "test-key")

	path := writeSettings(t, `engines:
  - name: wikipedia
    engine: wikipedia
    shortcut: wiki
    categories:
      - general
  - name: brave
    engine: brave
    shortcut: br
    categories:
      - general
`)

	svc, err := NewServiceFromFile(path, os.Getenv)
	if err != nil {
		t.Fatalf("NewServiceFromFile: %v", err)
	}

	var braveKey any
	for i := range svc.cfg.Engines {
		switch svc.cfg.Engines[i].Engine {
		case "brave":
			braveKey = svc.cfg.Engines[i].Overrides["api_key"]
		case "wikipedia":
			if _, ok := svc.cfg.Engines[i].Overrides["api_key"]; ok {
				t.Error("wikipedia must not receive an api_key override")
			}
		}
	}
	if braveKey != "test-key" {
		t.Errorf("brave Overrides[api_key] = %v, want test-key", braveKey)
	}
}

// TestNewServiceFromFileCustomGetenv verifies that a caller-provided getenv
// function is honored: keys injected only for the configured engine, and
// unknown engines never match a NANUQ_<ENGINE>_API_KEY variable.
func TestNewServiceFromFileCustomGetenv(t *testing.T) {
	path := writeSettings(t, `engines:
  - name: brave
    engine: brave
    shortcut: br
    categories:
      - general
  - name: startpage
    engine: startpage
    shortcut: sp
    categories:
      - general
`)

	svc, err := NewServiceFromFile(path, func(name string) string {
		if name == "NANUQ_BRAVE_API_KEY" {
			return "custom-key"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("NewServiceFromFile: %v", err)
	}

	for i := range svc.cfg.Engines {
		switch svc.cfg.Engines[i].Engine {
		case "brave":
			if got := svc.cfg.Engines[i].Overrides["api_key"]; got != "custom-key" {
				t.Errorf("brave Overrides[api_key] = %v, want custom-key", got)
			}
		case "startpage":
			if _, ok := svc.cfg.Engines[i].Overrides["api_key"]; ok {
				t.Error("startpage must not receive an api_key override")
			}
		}
	}
}

// TestNewServiceFromFileMissingFile verifies the error path: a non-existent
// cfgPath must surface as an error wrapping config.Load.
func TestNewServiceFromFileMissingFile(t *testing.T) {
	_, err := NewServiceFromFile(filepath.Join(t.TempDir(), "does-not-exist.yml"), os.Getenv)
	if err == nil {
		t.Fatal("NewServiceFromFile with missing cfgPath: want error, got nil")
	}
}

// TestNewServiceFromFileNilGetenv verifies the nil-getenv guard: nil must
// not panic and behaves as an always-empty getter — the OS environment is
// NOT consulted, so even a set NANUQ_BRAVE_API_KEY is not injected.
func TestNewServiceFromFileNilGetenv(t *testing.T) {
	t.Setenv("NANUQ_BRAVE_API_KEY", "must-not-leak")

	path := writeSettings(t, `engines:
  - name: brave
    engine: brave
    shortcut: br
    categories:
      - general
`)

	svc, err := NewServiceFromFile(path, nil)
	if err != nil {
		t.Fatalf("NewServiceFromFile with nil getenv: %v", err)
	}
	if svc == nil {
		t.Fatal("NewServiceFromFile returned nil Service")
	}

	for i := range svc.cfg.Engines {
		if svc.cfg.Engines[i].Engine == "brave" {
			if _, ok := svc.cfg.Engines[i].Overrides["api_key"]; ok {
				t.Error("nil getenv must not inject api_key from the environment")
			}
		}
	}
}

// TestFactoryRegistry verifies the TASK-017 accessor: Factory.Registry()
// exposes the internal registry with all 14 modules registered.
func TestFactoryRegistry(t *testing.T) {
	f, err := NewFactory()
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}
	reg := f.Registry()
	if reg == nil {
		t.Fatal("Registry() returned nil")
	}

	want := []string{
		"xpath", "json_engine",
		"wikipedia", "baidu", "duckduckgo", "bing", "bing_images",
		"bing_news", "bing_videos", "google", "brave", "qwant",
		"mojeek", "startpage",
	}
	names := reg.Names()
	if len(names) != len(want) {
		t.Fatalf("Registry has %d modules %v, want %d", len(names), names, len(want))
	}
	for _, name := range want {
		if !reg.Has(name) {
			t.Errorf("Registry missing module %q", name)
		}
	}
}
