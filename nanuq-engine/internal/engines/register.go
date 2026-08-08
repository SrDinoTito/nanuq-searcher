// Package-level aggregation of every engine module in internal/engines.
//
// RegisterAll is the single entry point used by the application bootstrap
// (TASK-011, DSG-003, DECISION-006, CA-006) to register all data-driven and
// core modules into the engine registry before serving starts.
package engines

import (
	"fmt"
	"sort"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
)

// RegisterAll registers every engine module (data-driven and core) into reg.
//
// The data-driven modules are registered first through their own Register
// functions (RegisterXPath for "xpath", Register for "json_engine"); the 12
// core modules follow through their constructors, iterated in sorted key
// order for deterministic registration (CA-006). Every error is wrapped with
// its module context (REQ-NF-007 %w).
func RegisterAll(reg *engine.Registry) error {
	if err := RegisterXPath(reg); err != nil {
		return fmt.Errorf("engines: register xpath: %w", err)
	}
	if err := Register(reg); err != nil {
		return fmt.Errorf("engines: register json_engine: %w", err)
	}

	constructors := map[string]func(*config.EngineConfig) (engine.Engine, error){
		"wikipedia":   NewWikipediaEngine,
		"baidu":       NewBaiduEngine,
		"duckduckgo":  NewDuckDuckGoEngine,
		"bing":        NewBingEngine,
		"bing_images": NewBingImagesEngine,
		"bing_news":   NewBingNewsEngine,
		"bing_videos": NewBingVideosEngine,
		"google":      NewGoogleEngine,
		"brave":       NewBraveEngine,
		"qwant":       NewQwantEngine,
		"mojeek":      NewMojeekEngine,
		"startpage":   NewStartpageEngine,
	}
	for _, name := range sortedKeys(constructors) {
		if err := reg.Register(name, constructors[name]); err != nil {
			return fmt.Errorf("engines: register %s: %w", name, err)
		}
	}
	return nil
}

// sortedKeys returns the keys of m in ascending lexicographic order, so that
// map iteration in callers is deterministic.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
