package config

import (
	"fmt"
	"os"
	"strconv"
)

// ApplyEnvOverrides applies REQ-NF-008 environment overrides on top of the
// file-loaded configuration. Only variables that are set (even to an empty
// value) override their file counterpart, matching `os.LookupEnv` semantics.
func ApplyEnvOverrides(cfg *Config) error {
	if v, ok := os.LookupEnv("PORT"); ok {
		port, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("config: env override PORT=%q: must be an integer: %w", v, err)
		}
		cfg.Server.Port = port
	}

	if v, ok := os.LookupEnv("BASE_URL"); ok {
		cfg.Server.BaseURL = FlexString(v)
	}

	if v, ok := os.LookupEnv("SECRET_KEY"); ok {
		cfg.Server.SecretKey = v
	}

	if v, ok := os.LookupEnv("VALKEY_URL"); ok {
		cfg.Valkey.URL = FlexString(v)
	}

	return nil
}
