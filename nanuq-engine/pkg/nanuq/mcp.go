package nanuq

import (
	"fmt"
	"strings"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/engine"
	"nanuq-engine/internal/engines"
)

// apiKeyEnv maps the engine modules that require an API key (D-04) to the
// environment variable that provides it, following the NANUQ_<ENGINE>_API_KEY
// convention (REQ-019).
var apiKeyEnv = map[string]string{
	"brave":  "NANUQ_BRAVE_API_KEY",
	"bing":   "NANUQ_BING_API_KEY",
	"google": "NANUQ_GOOGLE_API_KEY",
}

// NewServiceFromFile construye un Service listo para el MCP layer cargando
// la configuración desde un archivo YAML (formato settings del engine) y
// aplicando API keys desde variables de entorno.
//
// Usa el Load EXISTENTE de internal/config, que ya aplica ApplyEnvOverrides
// para PORT/BASE_URL/SECRET_KEY/VALKEY_URL. Además inyecta en
// cfg.Engines[i].Overrides["api_key"] el valor de NANUQ_<ENGINE>_API_KEY
// (ej: NANUQ_BRAVE_API_KEY) para los engines que requieren key
// (brave, bing, google — D-04). getenv debe ser os.Getenv en producción;
// un getenv nil se comporta como un getter siempre-vacío.
func NewServiceFromFile(cfgPath string, getenv func(string) string) (*Service, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("nanuq: load config %s: %w", cfgPath, err)
	}

	applyEngineKeys(cfg, getenv)

	reg := engine.New()
	if err := engines.RegisterAll(reg); err != nil {
		return nil, fmt.Errorf("nanuq: register engine modules: %w", err)
	}

	return NewService(cfg, reg)
}

// applyEngineKeys inyecta la API key de cada engine configurado que la
// requiere (brave, bing, google — D-04): el valor de NANUQ_<ENGINE>_API_KEY
// leído vía getenv se guarda en cfg.Engines[i].Overrides["api_key"]. El
// mapa Overrides se crea si es nil y las keys vacías se ignoran. Un getenv
// nil actúa como getter siempre-vacío (no se inyecta nada).
func applyEngineKeys(cfg *config.Config, getenv func(string) string) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	for i := range cfg.Engines {
		envName, ok := apiKeyEnv[strings.ToLower(cfg.Engines[i].Engine)]
		if !ok {
			continue
		}
		key := getenv(envName)
		if key == "" {
			continue
		}
		ecfg := &cfg.Engines[i]
		if ecfg.Overrides == nil {
			ecfg.Overrides = make(map[string]any)
		}
		ecfg.Overrides["api_key"] = key
	}
}

// Registry expone el Registry interno para casos que lo requieran
// (TASK-017 opcional). El MCP layer normalmente no lo necesita.
func (f *Factory) Registry() *engine.Registry {
	return f.reg
}
