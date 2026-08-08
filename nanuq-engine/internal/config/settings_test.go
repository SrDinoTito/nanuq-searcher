package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeTemp writes content to a fresh settings.yml under a temp dir and
// returns its path.
func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.yml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// clearEnv removes the REQ-NF-008 env vars for the duration of the test and
// restores their previous values via t.Cleanup. Tests are not parallel, so
// ambient env vars cannot leak across tests.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"PORT", "BASE_URL", "SECRET_KEY", "VALKEY_URL"} {
		old, had := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unsetenv %s: %v", key, err)
		}
		if had {
			t.Cleanup(func() { _ = os.Setenv(key, old) })
		} else {
			t.Cleanup(func() { _ = os.Unsetenv(key) })
		}
	}
}

const minimalYAML = `
general:
  instance_name: "minimal"

engines:
  - name: wikipedia
    engine: wikipedia
`

func TestLoadFromFile(t *testing.T) {
	clearEnv(t)

	cfg, err := Load(filepath.Join("testdata", "settings_test.yml"))
	if err != nil {
		t.Fatalf("Load(fixture) error: %v", err)
	}

	// general
	if cfg.General.InstanceName != "nanuq test" {
		t.Errorf("InstanceName = %q, want %q", cfg.General.InstanceName, "nanuq test")
	}
	if !cfg.General.Debug {
		t.Error("Debug = false, want true")
	}
	if cfg.General.PrivacypolicyURL != "https://nanuq.example/privacy" {
		t.Errorf("PrivacypolicyURL = %q", cfg.General.PrivacypolicyURL)
	}
	if cfg.General.DonationURL != "https://nanuq.example/donate" {
		t.Errorf("DonationURL = %q", cfg.General.DonationURL)
	}
	if cfg.General.ContactURL != "https://nanuq.example/contact" {
		t.Errorf("ContactURL = %q", cfg.General.ContactURL)
	}
	if !cfg.General.EnableMetrics {
		t.Error("EnableMetrics = false, want true")
	}

	// brand
	if cfg.Brand.DocsURL != "https://nanuq.example/docs" {
		t.Errorf("Brand.DocsURL = %q", cfg.Brand.DocsURL)
	}
	if cfg.Brand.IssueURL != "https://nanuq.example/issues" {
		t.Errorf("Brand.IssueURL = %q", cfg.Brand.IssueURL)
	}

	// search
	if cfg.Search.SafeSearch != 1 {
		t.Errorf("SafeSearch = %d, want 1", cfg.Search.SafeSearch)
	}
	if cfg.Search.Autocomplete != "duckduckgo" {
		t.Errorf("Autocomplete = %q", cfg.Search.Autocomplete)
	}
	if cfg.Search.BanTimeOnFail != 10 {
		t.Errorf("BanTimeOnFail = %d, want 10", cfg.Search.BanTimeOnFail)
	}
	if cfg.Search.MaxBanTimeOnFail != 300 {
		t.Errorf("MaxBanTimeOnFail = %d, want 300", cfg.Search.MaxBanTimeOnFail)
	}
	if got := cfg.Search.SuspendedTimes["github.com"]; got != 3600 {
		t.Errorf("SuspendedTimes[github.com] = %d, want 3600", got)
	}
	if want := []string{"html", "json", "rss"}; !reflect.DeepEqual([]string(cfg.Search.Formats), want) {
		t.Errorf("Formats = %v, want %v", cfg.Search.Formats, want)
	}

	// server
	if cfg.Server.Port != 9999 {
		t.Errorf("Port = %d, want 9999", cfg.Server.Port)
	}
	if cfg.Server.BindAddress != "0.0.0.0" {
		t.Errorf("BindAddress = %q", cfg.Server.BindAddress)
	}
	if cfg.Server.BaseURL != "" { // base_url: false → empty FlexString
		t.Errorf("BaseURL = %q, want empty", cfg.Server.BaseURL)
	}
	if !cfg.Server.Limiter || !cfg.Server.PublicInstance || !cfg.Server.ImageProxy {
		t.Errorf("Limiter/PublicInstance/ImageProxy = %v/%v/%v, want true", cfg.Server.Limiter, cfg.Server.PublicInstance, cfg.Server.ImageProxy)
	}
	if cfg.Server.SecretKey != "test-secret" {
		t.Errorf("SecretKey = %q", cfg.Server.SecretKey)
	}

	// valkey
	if cfg.Valkey.URL != "redis://127.0.0.1:6379/0" {
		t.Errorf("Valkey.URL = %q", cfg.Valkey.URL)
	}

	// ui
	if cfg.UI.DefaultTheme != "simple" || cfg.UI.DefaultLocale != "en" {
		t.Errorf("DefaultTheme/DefaultLocale = %q/%q", cfg.UI.DefaultTheme, cfg.UI.DefaultLocale)
	}
	if got := cfg.UI.ThemeArgs["simple_style"]; got != "auto" {
		t.Errorf("ThemeArgs[simple_style] = %v, want auto", got)
	}

	// preferences
	if want := []string{"language", "autocomplete"}; !reflect.DeepEqual(cfg.Preferences.Lock, want) {
		t.Errorf("Preferences.Lock = %v, want %v", cfg.Preferences.Lock, want)
	}

	// outgoing
	if cfg.Outgoing.RequestTimeout != 5.0 {
		t.Errorf("RequestTimeout = %v, want 5.0", cfg.Outgoing.RequestTimeout)
	}
	if cfg.Outgoing.MaxRequestTimeout != 10.0 {
		t.Errorf("MaxRequestTimeout = %v, want 10.0", cfg.Outgoing.MaxRequestTimeout)
	}
	if cfg.Outgoing.PoolConnections != 200 {
		t.Errorf("PoolConnections = %d, want 200", cfg.Outgoing.PoolConnections)
	}
	if cfg.Outgoing.PoolMaxsize != 50 {
		t.Errorf("PoolMaxsize = %d, want 50", cfg.Outgoing.PoolMaxsize)
	}
	if cfg.Outgoing.EnableHTTP2 {
		t.Error("EnableHTTP2 = true, want false")
	}
	if want := []string{"192.168.1.10"}; !reflect.DeepEqual(cfg.Outgoing.SourceIPs, want) {
		t.Errorf("SourceIPs = %v, want %v", cfg.Outgoing.SourceIPs, want)
	}

	// engines
	if len(cfg.Engines) != 2 {
		t.Fatalf("len(Engines) = %d, want 2", len(cfg.Engines))
	}
	e0 := cfg.Engines[0]
	if e0.Name != "wikipedia" || e0.Engine != "wikipedia" || e0.Shortcut != "wiki" {
		t.Errorf("engine[0] = %q/%q/%q", e0.Name, e0.Engine, e0.Shortcut)
	}
	if want := []string{"general"}; !reflect.DeepEqual([]string(e0.Categories), want) {
		t.Errorf("engine[0].Categories = %v, want %v (scalar form)", e0.Categories, want)
	}
	if e0.Weight != 1.0 {
		t.Errorf("engine[0].Weight = %v, want default 1.0", e0.Weight)
	}
	if got := e0.Overrides["search_url"]; got != "https://en.wikipedia.org/w/index.php?search={query}" {
		t.Errorf("engine[0].Overrides[search_url] = %v", got)
	}
	if got := e0.Overrides["results_xpath"]; got == "" || got == nil {
		t.Errorf("engine[0].Overrides[results_xpath] = %v, want non-empty", got)
	}
	if got := e0.Overrides["language"]; got != "en" {
		t.Errorf("engine[0].Overrides[language] = %v, want en", got)
	}
	if got := e0.Overrides["base_url"]; got != "https://en.wikipedia.org" {
		t.Errorf("engine[0].Overrides[base_url] = %v", got)
	}
	if _, ok := e0.Overrides["name"]; ok {
		t.Error("engine[0].Overrides contains 'name' (modeled key leaked into Overrides)")
	}

	e1 := cfg.Engines[1]
	if e1.Name != "duckduckgo_extra" {
		t.Errorf("engine[1].Name = %q", e1.Name)
	}
	if want := []string{"images"}; !reflect.DeepEqual([]string(e1.Categories), want) {
		t.Errorf("engine[1].Categories = %v, want %v (list form)", e1.Categories, want)
	}
	if !e1.Disabled {
		t.Error("engine[1].Disabled = false, want true")
	}
	if e1.Overrides["xpath"] == nil {
		t.Error("engine[1].Overrides[xpath] = nil, want nested map")
	}
}

func TestDefaults(t *testing.T) {
	clearEnv(t)

	cfg, err := Load(writeTemp(t, minimalYAML))
	if err != nil {
		t.Fatalf("Load(minimal) error: %v", err)
	}

	if cfg.General.InstanceName != "minimal" {
		t.Errorf("InstanceName = %q, want minimal", cfg.General.InstanceName)
	}
	if cfg.Server.Port != 8888 {
		t.Errorf("Port = %d, want default 8888", cfg.Server.Port)
	}
	if cfg.Server.BindAddress != "127.0.0.1" {
		t.Errorf("BindAddress = %q, want default 127.0.0.1", cfg.Server.BindAddress)
	}
	if cfg.Search.BanTimeOnFail != 5 {
		t.Errorf("BanTimeOnFail = %d, want default 5", cfg.Search.BanTimeOnFail)
	}
	if cfg.Search.MaxBanTimeOnFail != 120 {
		t.Errorf("MaxBanTimeOnFail = %d, want default 120", cfg.Search.MaxBanTimeOnFail)
	}
	if cfg.Outgoing.RequestTimeout != 3.0 {
		t.Errorf("RequestTimeout = %v, want default 3.0", cfg.Outgoing.RequestTimeout)
	}
	if cfg.Outgoing.PoolConnections != 100 {
		t.Errorf("PoolConnections = %d, want default 100", cfg.Outgoing.PoolConnections)
	}
	if cfg.Outgoing.PoolMaxsize != 20 {
		t.Errorf("PoolMaxsize = %d, want default 20", cfg.Outgoing.PoolMaxsize)
	}
	if !cfg.Outgoing.EnableHTTP2 {
		t.Error("EnableHTTP2 = false, want default true")
	}
	if len(cfg.Engines) != 1 {
		t.Fatalf("len(Engines) = %d, want 1", len(cfg.Engines))
	}
	if cfg.Engines[0].Weight != 1.0 {
		t.Errorf("engine Weight = %v, want default 1.0", cfg.Engines[0].Weight)
	}
	if cfg.Engines[0].Timeout != 0 {
		t.Errorf("engine Timeout = %v, want 0 (use global request_timeout)", cfg.Engines[0].Timeout)
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("PORT", "7777")
	t.Setenv("BASE_URL", "https://nanuq.example")
	t.Setenv("SECRET_KEY", "env-secret")
	t.Setenv("VALKEY_URL", "redis://valkey:6379/1")

	cfg, err := Load(writeTemp(t, minimalYAML))
	if err != nil {
		t.Fatalf("Load(minimal) error: %v", err)
	}

	if cfg.Server.Port != 7777 {
		t.Errorf("Port = %d, want env override 7777", cfg.Server.Port)
	}
	if cfg.Server.BaseURL != "https://nanuq.example" {
		t.Errorf("BaseURL = %q, want env override", cfg.Server.BaseURL)
	}
	if cfg.Server.SecretKey != "env-secret" {
		t.Errorf("SecretKey = %q, want env override", cfg.Server.SecretKey)
	}
	if cfg.Valkey.URL != "redis://valkey:6379/1" {
		t.Errorf("Valkey.URL = %q, want env override", cfg.Valkey.URL)
	}
}

func TestEnvOverrideInvalidPort(t *testing.T) {
	t.Setenv("PORT", "abc")

	_, err := Load(writeTemp(t, minimalYAML))
	if err == nil {
		t.Fatal("Load with invalid PORT = nil error, want error")
	}
	if !strings.Contains(err.Error(), "PORT") || !strings.Contains(err.Error(), "integer") {
		t.Errorf("error = %q, want context mentioning PORT and integer", err)
	}
}

func TestInvalidYAML(t *testing.T) {
	clearEnv(t)

	_, err := Load(writeTemp(t, "server:\n  port: not-a-number\n"))
	if err == nil {
		t.Fatal("Load(broken YAML) = nil error, want error")
	}
	if !strings.Contains(err.Error(), "config: load") {
		t.Errorf("error = %q, want context prefix 'config: load'", err)
	}
}

func TestMissingFile(t *testing.T) {
	clearEnv(t)

	_, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yml"))
	if err == nil {
		t.Fatal("Load(missing file) = nil error, want error")
	}
	if !strings.Contains(err.Error(), "config: load") {
		t.Errorf("error = %q, want context prefix 'config: load'", err)
	}
}

func TestFlexStringFalse(t *testing.T) {
	clearEnv(t)

	cfg, err := Load(writeTemp(t, `
server:
  base_url: false

valkey:
  url: false
`))
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if cfg.Server.BaseURL != "" {
		t.Errorf("BaseURL = %q, want empty (base_url: false)", cfg.Server.BaseURL)
	}
	if cfg.Valkey.URL != "" {
		t.Errorf("Valkey.URL = %q, want empty (url: false)", cfg.Valkey.URL)
	}
}
