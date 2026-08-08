package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefault(t *testing.T) {
	c := Default()

	if c.Fetch.TimeoutSec != DefaultFetchTimeoutSec {
		t.Errorf("fetch timeout = %d, want %d", c.Fetch.TimeoutSec, DefaultFetchTimeoutSec)
	}
	if c.Fetch.MaxBytes != DefaultFetchMaxBytes {
		t.Errorf("fetch max_bytes = %d, want %d", c.Fetch.MaxBytes, DefaultFetchMaxBytes)
	}
	if c.Fetch.MaxRedirects != DefaultFetchMaxRedirects {
		t.Errorf("fetch max_redirects = %d, want %d", c.Fetch.MaxRedirects, DefaultFetchMaxRedirects)
	}
	if c.Crawl.Workers != DefaultCrawlWorkers {
		t.Errorf("crawl workers = %d, want %d", c.Crawl.Workers, DefaultCrawlWorkers)
	}
	if c.Crawl.MaxPages != DefaultCrawlMaxPages {
		t.Errorf("crawl max_pages = %d, want %d", c.Crawl.MaxPages, DefaultCrawlMaxPages)
	}
	if c.Crawl.MaxDepth != DefaultCrawlMaxDepth {
		t.Errorf("crawl max_depth = %d, want %d", c.Crawl.MaxDepth, DefaultCrawlMaxDepth)
	}
	if c.Crawl.TimeoutSec != DefaultCrawlTimeoutSec {
		t.Errorf("crawl timeout = %d, want %d", c.Crawl.TimeoutSec, DefaultCrawlTimeoutSec)
	}
	if c.Search.MaxResults != DefaultSearchMaxResults {
		t.Errorf("search max_results = %d, want %d", c.Search.MaxResults, DefaultSearchMaxResults)
	}
	if c.UserAgent != DefaultUserAgent {
		t.Errorf("user_agent = %q, want %q", c.UserAgent, DefaultUserAgent)
	}
}

func TestLoad(t *testing.T) {
	path := writeYAML(t, `
fetch:
  timeout_sec: 45
  max_bytes: 1048576
  max_redirects: 3
crawl:
  workers: 4
  max_pages: 50
  max_depth: 2
  timeout_sec: 20
search:
  max_results: 20
user_agent: "test-ua"
`)
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if c.Fetch.TimeoutSec != 45 || c.Fetch.MaxBytes != 1048576 || c.Fetch.MaxRedirects != 3 {
		t.Errorf("fetch = %+v, want 45/1048576/3", c.Fetch)
	}
	if c.Crawl.Workers != 4 || c.Crawl.MaxPages != 50 || c.Crawl.MaxDepth != 2 || c.Crawl.TimeoutSec != 20 {
		t.Errorf("crawl = %+v, want 4/50/2/20", c.Crawl)
	}
	if c.Search.MaxResults != 20 {
		t.Errorf("search = %+v, want MaxResults 20", c.Search)
	}
	if c.UserAgent != "test-ua" {
		t.Errorf("user_agent = %q, want %q", c.UserAgent, "test-ua")
	}
}

func TestLoadAppliesDefaultsOnMissingSections(t *testing.T) {
	// Only one key set: everything else must keep code-first defaults.
	path := writeYAML(t, "fetch:\n  timeout_sec: 42\n")
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if c.Fetch.TimeoutSec != 42 {
		t.Errorf("fetch timeout = %d, want 42", c.Fetch.TimeoutSec)
	}
	if c.Fetch.MaxBytes != DefaultFetchMaxBytes {
		t.Errorf("fetch max_bytes = %d, want default %d", c.Fetch.MaxBytes, DefaultFetchMaxBytes)
	}
	if c.Crawl.Workers != DefaultCrawlWorkers {
		t.Errorf("crawl workers = %d, want default %d", c.Crawl.Workers, DefaultCrawlWorkers)
	}
	if c.Crawl.MaxDepth != DefaultCrawlMaxDepth {
		t.Errorf("crawl max_depth = %d, want default %d", c.Crawl.MaxDepth, DefaultCrawlMaxDepth)
	}
	if c.UserAgent != DefaultUserAgent {
		t.Errorf("user_agent = %q, want default", c.UserAgent)
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yml")); err == nil {
		t.Fatal("Load on missing file: expected error, got nil")
	}
}

func TestValidate(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("default config must validate: %v", err)
	}
}

func TestValidateMaxBytesOutOfRange(t *testing.T) {
	cases := []struct {
		name string
		max  int64
	}{
		{"below 64KiB", 1024},
		{"above 10MiB", 100 << 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			c.Fetch.MaxBytes = tc.max
			if err := c.Validate(); err == nil {
				t.Fatalf("max_bytes=%d: expected validation error, got nil", tc.max)
			}
		})
	}
}

func TestValidateCrawlBounds(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"workers 0", func(c *Config) { c.Crawl.Workers = 0 }},
		{"workers 65", func(c *Config) { c.Crawl.Workers = 65 }},
		{"max_pages 0", func(c *Config) { c.Crawl.MaxPages = 0 }},
		{"max_pages 10001", func(c *Config) { c.Crawl.MaxPages = 10001 }},
		{"max_depth 0", func(c *Config) { c.Crawl.MaxDepth = 0 }},
		{"max_depth 11", func(c *Config) { c.Crawl.MaxDepth = 11 }},
		{"timeout 0", func(c *Config) { c.Crawl.TimeoutSec = 0 }},
		{"fetch timeout 0", func(c *Config) { c.Fetch.TimeoutSec = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			tc.mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatalf("%s: expected validation error, got nil", tc.name)
			}
		})
	}
}

func TestEnvOverride(t *testing.T) {
	t.Setenv("NANUQ_FETCH_TIMEOUT", "55")
	t.Setenv("NANUQ_FETCH_MAX_BYTES", "4194304")
	t.Setenv("NANUQ_CRAWL_WORKERS", "16")
	t.Setenv("NANUQ_CRAWL_MAX_PAGES", "500")
	t.Setenv("NANUQ_CRAWL_MAX_DEPTH", "4")
	t.Setenv("NANUQ_UA", "test-agent/1.0")

	path := writeYAML(t, "")
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if c.Fetch.TimeoutSec != 55 {
		t.Errorf("fetch timeout = %d, want env 55", c.Fetch.TimeoutSec)
	}
	if c.Fetch.MaxBytes != 4194304 {
		t.Errorf("fetch max_bytes = %d, want env 4194304", c.Fetch.MaxBytes)
	}
	if c.Crawl.Workers != 16 {
		t.Errorf("crawl workers = %d, want env 16", c.Crawl.Workers)
	}
	if c.Crawl.MaxPages != 500 {
		t.Errorf("crawl max_pages = %d, want env 500", c.Crawl.MaxPages)
	}
	if c.Crawl.MaxDepth != 4 {
		t.Errorf("crawl max_depth = %d, want env 4", c.Crawl.MaxDepth)
	}
	if c.UserAgent != "test-agent/1.0" {
		t.Errorf("user_agent = %q, want env value", c.UserAgent)
	}
	// Unset vars keep defaults.
	if c.Fetch.MaxRedirects != DefaultFetchMaxRedirects {
		t.Errorf("max_redirects = %d, want default %d (env not set)", c.Fetch.MaxRedirects, DefaultFetchMaxRedirects)
	}
	if c.Search.MaxResults != DefaultSearchMaxResults {
		t.Errorf("max_results = %d, want default %d (env not set)", c.Search.MaxResults, DefaultSearchMaxResults)
	}
}

func TestEnvOverrideInvalidInteger(t *testing.T) {
	t.Setenv("NANUQ_CRAWL_WORKERS", "lots")
	path := writeYAML(t, "")
	if _, err := Load(path); err == nil {
		t.Fatal("invalid env int: expected error, got nil")
	}
}

func TestEmbeddedSettingsParse(t *testing.T) {
	data := EmbeddedSettings()
	if len(data) == 0 {
		t.Fatal("embedded settings empty")
	}
	if !strings.Contains(string(data), "engine: wikipedia") {
		t.Error("embedded settings missing wikipedia engine entry")
	}
}

func writeYAML(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}
