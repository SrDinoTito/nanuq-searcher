// Command nanuq-mcp is the MCP server for nanuq-searcher-mcp (spec
// nanuq-searcher-mcp, CST-008). It wires the search Service from the engine
// settings YAML (REQ-016/REQ-017), applies the validated MCP config
// (DSG-012 bounds), and exposes the three tools implemented in TASK-006
// (search), TASK-010 (fetch) and TASK-013 (map) over stdio.
package main

import (
	"flag"
	"log/slog"
	"os"
	"sort"

	"gopkg.in/yaml.v3"

	"nanuq-engine/pkg/nanuq"
	"nanuq-searcher-mcp/internal/config"
	"nanuq-searcher-mcp/internal/mcp"
)

const version = "0.1.0"

// settingsFile mirrors the subset of the engine settings YAML (engines
// section) needed for the REQ-017 startup log. The authoritative parse of
// the full file happens inside nanuq.NewServiceFromFile.
type settingsFile struct {
	Engines []settingsEngine `yaml:"engines"`
}

type settingsEngine struct {
	Name       string   `yaml:"name"`
	Module     string   `yaml:"engine"`
	Categories []string `yaml:"categories"`
	Disabled   bool     `yaml:"disabled"`
	Inactive   bool     `yaml:"inactive"`
}

func main() {
	logger := slog.Default()

	cfgPath := flag.String("config", "",
		"path to nanuq-engine settings YAML (default: embedded settings.default.yml)")
	flag.Parse()

	settings := config.EmbeddedSettings()

	if *cfgPath == "" {
		path, err := writeTempSettings(settings)
		if err != nil {
			logger.Error("write embedded settings to temp file", "error", err)
			os.Exit(1)
		}
		defer func() { _ = os.Remove(path) }()
		*cfgPath = path
	}

	logEnabledEngines(logger, settings)

	svc, err := nanuq.NewServiceFromFile(*cfgPath, os.Getenv)
	if err != nil {
		logger.Error("nanuq service init failed", "config", *cfgPath, "error", err)
		os.Exit(1)
	}
	// MCP config: defaults + NANUQ_* env overrides (no file; the engine
	// settings YAML is a separate config handled above via -config).
	cfgMCP, err := config.Load("")
	if err != nil {
		logger.Error("MCP config init failed", "error", err)
		os.Exit(1)
	}
	// Global bounds validation (DSG-012): fail fast with exit 1 if any
	// configured limit (fetch bytes/redirects, crawl workers/pages/depth,
	// search results) is out of range.
	if err := cfgMCP.Validate(); err != nil {
		logger.Error("MCP config validation failed", "error", err)
		os.Exit(1)
	}
	srv := mcp.NewServer(svc, &cfgMCP, logger)

	logger.Info("nanuq-searcher-mcp ready", "version", version)
	if err := mcp.ServeStdio(srv); err != nil {
		logger.Error("MCP server terminated", "error", err)
		os.Exit(1)
	}
}

// writeTempSettings writes the embedded settings YAML to a temp file and
// returns its path. REQ-016: no -config flag => the embedded default is
// materialized on disk so nanuq.NewServiceFromFile can read it.
func writeTempSettings(data []byte) (string, error) {
	f, err := os.CreateTemp("", "nanuq-settings-*.yml")
	if err != nil {
		return "", err
	}
	path := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

// logEnabledEngines parses the embedded settings (yaml.v3, only for the
// log) and logs the enabled engines grouped by category (REQ-017). A parse
// failure is non-fatal: the engine's own validation still runs inside
// NewServiceFromFile and would surface as a fatal startup error.
func logEnabledEngines(logger *slog.Logger, data []byte) {
	var sf settingsFile
	if err := yaml.Unmarshal(data, &sf); err != nil {
		logger.Warn("parse embedded settings for startup log", "error", err)
		return
	}

	byCategory := make(map[string][]string)
	for _, e := range sf.Engines {
		if e.Disabled || e.Inactive {
			continue
		}
		for _, cat := range e.Categories {
			byCategory[cat] = append(byCategory[cat], e.Name)
		}
	}

	cats := make([]string, 0, len(byCategory))
	for cat := range byCategory {
		cats = append(cats, cat)
	}
	sort.Strings(cats)

	for _, cat := range cats {
		logger.Info("engine category enabled", "category", cat, "engines", byCategory[cat])
	}
}
