package nanuq

import (
	"fmt"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
	"nanuq-engine/internal/engines"
)

// Factory instantiates engine modules registered in the shared registry
// (DSG-018). It wraps internal/engine.Registry: module registration
// (NewFactory) and instance construction (Instantiate) without exposing
// the internal registry type.
type Factory struct {
	reg *engine.Registry
}

// NewFactory creates a Factory with every engine module registered
// (14 modules: xpath, json_engine and the 12 core engines).
func NewFactory() (*Factory, error) {
	reg := engine.New()
	if err := engines.RegisterAll(reg); err != nil {
		return nil, fmt.Errorf("nanuq: register engine modules: %w", err)
	}
	return &Factory{reg: reg}, nil
}

// EngineInfo describes a registered engine module.
type EngineInfo struct {
	// Kind is the registered module name, e.g. "xpath", "json_engine",
	// "duckduckgo".
	Kind string
}

// List returns the registered engine modules, sorted by module name
// (internal/engine.Registry.Names already sorts ascending).
func (f *Factory) List() []EngineInfo {
	names := f.reg.Names()
	infos := make([]EngineInfo, 0, len(names))
	for _, name := range names {
		infos = append(infos, EngineInfo{Kind: name})
	}
	return infos
}

// Instantiate builds one engine instance from its module configuration.
//
// The error wraps engine.ErrEngineNotFound when cfg.Engine is not a
// registered module, and engine.ErrInvalidConfig (via the module factory)
// when the module's required overrides are missing or malformed.
func (f *Factory) Instantiate(cfg config.EngineConfig) (Engine, error) {
	if !f.reg.Has(cfg.Engine) {
		return Engine{}, fmt.Errorf("nanuq: instantiate %q: %w", cfg.Engine, engine.ErrEngineNotFound)
	}
	impl, err := f.reg.Instantiate(cfg)
	if err != nil {
		return Engine{}, fmt.Errorf("nanuq: instantiate %q: %w", cfg.Engine, err)
	}
	return Engine{
		Name:       impl.Name(),
		Shortcut:   impl.Shortcut(),
		Categories: append([]string(nil), impl.Categories()...),
		Kind:       cfg.Engine,
	}, nil
}
