package engine

import (
	"fmt"
	"sort"

	"nanuq-engine/internal/config"
)

// Factory creates a new Engine instance for a single YAML entry (DSG-003,
// REQ-004, DECISION-005). One module (e.g. "duckduckgo") registers a single
// factory; Instantiate invokes it once per YAML entry so a module can yield
// multiple instances (duckduckgo_extra → images/videos/news). Factories should
// return an error wrapping ErrInvalidConfig when the config is unusable.
type Factory func(cfg *config.EngineConfig) (Engine, error)

// Registry holds one Factory per engine MODULE (REQ-004): the 1:N mapping
// between module names and YAML instances is resolved at Instantiate time.
// Duplicate module registrations are rejected to catch wiring bugs early.
type Registry struct {
	factories map[string]Factory
}

// New returns an empty Registry ready for use.
func New() *Registry {
	return &Registry{factories: make(map[string]Factory)}
}

// Register binds a module name (the EngineConfig.Engine field) to a factory.
// It returns an error when the module name is empty, the factory is nil, or
// the module is already registered.
func (r *Registry) Register(moduleName string, f Factory) error {
	if moduleName == "" {
		return fmt.Errorf("engine: cannot register an empty module name")
	}
	if f == nil {
		return fmt.Errorf("engine: cannot register a nil factory for module %q", moduleName)
	}
	if _, ok := r.factories[moduleName]; ok {
		return fmt.Errorf("engine: module %q is already registered", moduleName)
	}
	r.factories[moduleName] = f
	return nil
}

// Has reports whether a module factory is registered.
func (r *Registry) Has(moduleName string) bool {
	_, ok := r.factories[moduleName]
	return ok
}

// Instantiate creates one Engine instance for a single YAML entry by invoking
// the factory of the module named by cfg.Engine. It returns an error wrapping
// ErrEngineNotFound when the module is not registered (REQ-004: warn and skip
// unknown modules instead of failing the whole configuration).
func (r *Registry) Instantiate(cfg config.EngineConfig) (Engine, error) {
	f, ok := r.factories[cfg.Engine]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrEngineNotFound, cfg.Engine)
	}
	return f(&cfg)
}

// Names returns the sorted list of registered module names.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
