// Command nanuq-server is the entry point of the nanuq search engine.
//
// TASK-022 (final wiring): builds the full dependency graph — engine
// registry, bang store, engine catalog, network client, search service,
// webapp — and starts the HTTP server.
package main

import (
	"flag"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"

	"nanuq-engine/internal/bang"
	"nanuq-engine/internal/cache"
	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
	"nanuq-engine/internal/engines"
	"nanuq-engine/internal/limiter"
	"nanuq-engine/internal/metrics"
	"nanuq-engine/internal/network"
	"nanuq-engine/internal/search"
	"nanuq-engine/internal/webapp"
)

func main() {
	cfgPath := flag.String("config", "settings.yml", "path to the settings YAML file")
	flag.Parse()

	// REQ-NF-007: structured logging to stderr via log/slog.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		slog.Error("config load failed", "path", *cfgPath, "error", err)
		os.Exit(1)
	}

	// 1. Engine registry: the 14 modules (xpath, json_engine + 12 core).
	reg := engine.New()
	if err := engines.RegisterAll(reg); err != nil {
		slog.Error("engine registration failed", "error", err)
		os.Exit(1)
	}

	// 2. Bang store (embedded data/external_bangs.json).
	store := bang.New()
	if err := store.LoadEmbedded(); err != nil {
		slog.Error("embedded bang store load failed", "error", err)
		os.Exit(1)
	}

	// 3. Engine catalog: populated from the enabled engines in cfg.Engines.
	// Only engines that will actually run (not Disabled/Inactive) are listed,
	// so category routing and shortcut resolution never target dead engines.
	catalog := &search.RegistryCatalog{
		Reg:        reg,
		Shortcuts:  make(map[string]string),
		Engines:    make(map[string]bool),
		Categories: make(map[string][]string),
	}
	for _, ecfg := range cfg.Engines {
		if ecfg.Disabled || ecfg.Inactive {
			continue
		}
		name := strings.ToLower(ecfg.Name)
		if name == "" {
			continue
		}
		if ecfg.Shortcut != "" {
			catalog.Shortcuts[strings.ToLower(ecfg.Shortcut)] = name
		}
		catalog.Engines[name] = true
		// "all" lists every enabled engine (SearXNG convention): plain
		// queries with no bang/category target every enabled engine.
		catalog.Categories["all"] = append(catalog.Categories["all"], name)
		for _, cat := range ecfg.Categories {
			c := strings.ToLower(cat)
			catalog.Categories[c] = append(catalog.Categories[c], name)
		}
	}

	// 4. Network client (search.Requester).
	requester, err := network.New(cfg)
	if err != nil {
		slog.Error("network client init failed", "error", err)
		os.Exit(1)
	}

	// 5. Search service (processors are created only for enabled engines).
	svc := search.New(reg, store, catalog, cfg, requester, nil)

	// 6. Webapp HTTP front end.
	srv := webapp.New(cfg, svc, store, catalog)

	// 7. Optional rate limiter (REQ-016).
	if cfg.Server.Limiter {
		secret, err := cache.NewSecret(cfg.Server.SecretKey)
		if err != nil {
			slog.Error("limiter secret init failed", "error", err)
			os.Exit(1)
		}
		lim, err := limiter.New(limiter.Config{
			Enabled:        true,
			PublicInstance: cfg.Server.PublicInstance,
			ValkeyURL:      string(cfg.Valkey.URL),
			Secret:         secret,
		})
		if err != nil {
			slog.Error("limiter init failed", "error", err)
			os.Exit(1)
		}
		srv.WithLimiter(lim)
	}

	// 8. Optional Prometheus metrics endpoint (REQ-014).
	if cfg.General.EnableMetrics {
		metrics.New() // registers collectors on prometheus.DefaultRegisterer
		srv.RegisterMetrics(metrics.Handler())
	}

	addr := net.JoinHostPort(cfg.Server.BindAddress, strconv.Itoa(cfg.Server.Port))
	slog.Info("nanuq-server listening",
		"addr", addr,
		"instance", cfg.General.InstanceName,
		"engines", len(reg.Names()),
	)
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("server stopped", "addr", addr, "error", err)
		os.Exit(1)
	}
}
