// Package config is the MCP-layer configuration (DSG-010), distinct from
// nanuq-engine's internal/config (which is not importable from this module).
//
// It provides:
//   - code-first defaults (Default) for fetch/crawl/search limits and the
//     MCP user agent (REQ-016);
//   - typed YAML loading (Load) that applies defaults over the file;
//   - validation with ozzo-validation/v4 (DSG-011);
//   - NANUQ_* environment overrides applied on top of the file.
//
// The engine's own settings (settings.default.yml, embedded via go:embed —
// see embed.go) are a separate concern: they configure nanuq-engine and are
// consumed by nanuq.NewServiceFromFile.
package config

import (
	"fmt"
	"os"
	"strconv"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"gopkg.in/yaml.v3"
)

// Code-first defaults (DSG-010).
const (
	// DefaultFetchTimeoutSec is the default per-request fetch timeout (30s).
	DefaultFetchTimeoutSec = 30
	// DefaultFetchMaxBytes is the default response size cap (2 MiB).
	DefaultFetchMaxBytes = 2 << 20
	// DefaultFetchMaxRedirects is the default redirect budget (REQ-010: máx 5).
	DefaultFetchMaxRedirects = 5

	// DefaultCrawlWorkers is the default crawler worker-pool size (REQ-012).
	DefaultCrawlWorkers = 8
	// DefaultCrawlMaxPages is the default per-map page cap (RSK-004).
	DefaultCrawlMaxPages = 100
	// DefaultCrawlMaxDepth is the default BFS depth cap.
	DefaultCrawlMaxDepth = 3
	// DefaultCrawlTimeoutSec is the default per-request crawl timeout (EC-006).
	DefaultCrawlTimeoutSec = 15

	// DefaultSearchMaxResults is the default max_results for nanuq_search
	// (REQ-002: cap 1..50).
	DefaultSearchMaxResults = 10

	// DefaultUserAgent is the identifiable MCP user agent (NFR-003).
	DefaultUserAgent = "nanuq-mcp/0.1 (+https://github.com/srDino/nanuq-sercher)"

	// Validation bounds (DSG-011 / REQ-008 / REQ-012).
	minMaxBytes  = 64 << 10 // 64 KiB
	maxMaxBytes  = 10 << 20 // 10 MiB
	maxRedirects = 10
	maxWorkers   = 64
	maxPages     = 10000
	maxDepth     = 10
	maxResults   = 50
)

// Config is the root of the MCP configuration tree. YAML keys mirror the
// DSG-010 shape; fields absent from the file keep the code-first defaults.
type Config struct {
	Fetch     FetchConfig  `yaml:"fetch"`
	Crawl     CrawlConfig  `yaml:"crawl"`
	Search    SearchConfig `yaml:"search"`
	UserAgent string       `yaml:"user_agent"`
}

// FetchConfig holds the fetch guardrails (REQ-010).
type FetchConfig struct {
	TimeoutSec   int   `yaml:"timeout_sec"`
	MaxBytes     int64 `yaml:"max_bytes"`
	MaxRedirects int   `yaml:"max_redirects"`
}

// CrawlConfig holds the crawler limits (REQ-012, EC-006).
type CrawlConfig struct {
	Workers    int `yaml:"workers"`
	MaxPages   int `yaml:"max_pages"`
	MaxDepth   int `yaml:"max_depth"`
	TimeoutSec int `yaml:"timeout_sec"`
}

// SearchConfig holds the search tool defaults (REQ-002).
type SearchConfig struct {
	MaxResults int `yaml:"max_results"`
}

// Default returns a Config seeded with code-first defaults (DSG-010). It
// never fails and needs no I/O.
func Default() Config {
	return Config{
		Fetch: FetchConfig{
			TimeoutSec:   DefaultFetchTimeoutSec,
			MaxBytes:     DefaultFetchMaxBytes,
			MaxRedirects: DefaultFetchMaxRedirects,
		},
		Crawl: CrawlConfig{
			Workers:    DefaultCrawlWorkers,
			MaxPages:   DefaultCrawlMaxPages,
			MaxDepth:   DefaultCrawlMaxDepth,
			TimeoutSec: DefaultCrawlTimeoutSec,
		},
		Search: SearchConfig{
			MaxResults: DefaultSearchMaxResults,
		},
		UserAgent: DefaultUserAgent,
	}
}

// Load reads the YAML file at path into a Config seeded with Default(),
// applies NANUQ_* environment overrides on top and returns the result. A
// missing or unparsable file is an error; sections omitted from the file
// keep their defaults (code-first, same pattern as the engine's config.Load).
//
// An empty path is the "no file" case (used by cmd/nanuq-mcp for the MCP
// config, TASK-010): Load("") returns Default() plus the NANUQ_* env
// overrides only, without touching the filesystem.
func Load(path string) (Config, error) {
	c := Default()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("config: load %s: %w", path, err)
		}
		if err := yaml.Unmarshal(data, &c); err != nil {
			return Config{}, fmt.Errorf("config: parse %s: %w", path, err)
		}
	}

	if err := c.applyEnv(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Validate checks the MCP config bounds with ozzo-validation/v4 (DSG-011):
// max_bytes 64KiB..10MiB, workers 1..64, max_pages 1..10000, max_depth 1..10,
// max_results 1..50, timeouts > 0 and redirects 0..10. Nested structs are
// validated through their own Validate() (validation.Validatable, invoked by
// ozzo when a Field has no rules). It returns a combined
// validation.ValidationError when any bound is violated.
func (c Config) Validate() error {
	return validation.ValidateStruct(&c,
		validation.Field(&c.Fetch),
		validation.Field(&c.Crawl),
		validation.Field(&c.Search),
	)
}

// Validate checks the fetch guardrails (REQ-010).
func (f FetchConfig) Validate() error {
	return validation.ValidateStruct(&f,
		validation.Field(&f.TimeoutSec, validation.Min(1)),
		validation.Field(&f.MaxBytes, validation.Min(minMaxBytes), validation.Max(maxMaxBytes)),
		validation.Field(&f.MaxRedirects, validation.Min(0), validation.Max(maxRedirects)),
	)
}

// Validate checks the crawler limits (REQ-012, EC-006).
func (c CrawlConfig) Validate() error {
	return validation.ValidateStruct(&c,
		validation.Field(&c.Workers, validation.Min(1), validation.Max(maxWorkers)),
		validation.Field(&c.MaxPages, validation.Min(1), validation.Max(maxPages)),
		validation.Field(&c.MaxDepth, validation.Min(1), validation.Max(maxDepth)),
		validation.Field(&c.TimeoutSec, validation.Min(1)),
	)
}

// Validate checks the search tool defaults (REQ-002).
func (s SearchConfig) Validate() error {
	return validation.ValidateStruct(&s,
		validation.Field(&s.MaxResults, validation.Min(1), validation.Max(maxResults)),
	)
}

// applyEnv applies the NANUQ_* environment overrides (DSG-010, prefix
// NANUQ_). Only variables that are set (os.LookupEnv semantics, matching the
// engine's ApplyEnvOverrides) override their file/default counterpart. A set
// but non-integer value is an error.
func (c *Config) applyEnv() error {
	for _, e := range []struct {
		env string
		dst *int
	}{
		{"NANUQ_FETCH_TIMEOUT", &c.Fetch.TimeoutSec},
		{"NANUQ_FETCH_MAX_REDIRECTS", &c.Fetch.MaxRedirects},
		{"NANUQ_CRAWL_WORKERS", &c.Crawl.Workers},
		{"NANUQ_CRAWL_MAX_PAGES", &c.Crawl.MaxPages},
		{"NANUQ_CRAWL_MAX_DEPTH", &c.Crawl.MaxDepth},
		{"NANUQ_CRAWL_TIMEOUT", &c.Crawl.TimeoutSec},
		{"NANUQ_SEARCH_MAX_RESULTS", &c.Search.MaxResults},
	} {
		if v, ok := os.LookupEnv(e.env); ok {
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("config: env %s=%q: must be an integer: %w", e.env, v, err)
			}
			*e.dst = n
		}
	}

	if v, ok := os.LookupEnv("NANUQ_FETCH_MAX_BYTES"); ok {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("config: env NANUQ_FETCH_MAX_BYTES=%q: must be an integer: %w", v, err)
		}
		c.Fetch.MaxBytes = n
	}

	if v, ok := os.LookupEnv("NANUQ_UA"); ok {
		c.UserAgent = v
	}
	return nil
}
