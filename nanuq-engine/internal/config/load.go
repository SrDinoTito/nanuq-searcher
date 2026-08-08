package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Load reads and parses the settings file at cfgPath and applies environment
// overrides on top (REQ-NF-008).
//
// Defaults are seeded by newConfig() (code-first, DSG-007); the YAML file only
// overrides them. yaml.Unmarshal is used (not KnownFields): sections not yet
// modeled in FASE A (plugins, categories_as_tabs, ...) are ignored silently.
func Load(cfgPath string) (*Config, error) {
	cfg := newConfig()

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("config: load %s: %w", cfgPath, err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("config: load %s: %w", cfgPath, err)
	}

	applyEngineDefaults(cfg)

	if err := ApplyEnvOverrides(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// applyEngineDefaults fills code-first defaults for per-engine fields that a
// zero value cannot distinguish from an explicit "not set" (DSG-007). A
// weight of 0 would zero out the engine score, so explicit 0 is treated as
// the default 1.0. Timeout stays 0: it means "use the global request_timeout"
// in the processor (TASK-006).
func applyEngineDefaults(cfg *Config) {
	for i := range cfg.Engines {
		if cfg.Engines[i].Weight == 0 {
			cfg.Engines[i].Weight = 1.0
		}
	}
}
