# nanuq-engine

Meta-buscador escrito en Go (rediseño de SearXNG): búsqueda concurrente, privada y con timeouts sobre múltiples engines web.

## Características

- **Búsqueda concurrente con timeouts**: goroutines + `errgroup`, timeout global por búsqueda y timeout per-engine con watchdog (`internal/search`).
- **Engines data-driven**: motor XPath 1.0 (`xpath`) y mini-lenguaje JSON path (`json_engine`), configurables desde YAML sin código nuevo.
- **Bangs**: 13,561 bangs embebidos (`external_bangs.json`) con resolución de `!bang` en la query.
- **Rate limiting por IP**: ventana BURST (20s/15), LONG (600s/150) y API (3600s/4), fail-closed sin cabeceras XFF (`internal/limiter`).
- **Caché dual**: `ExpireCache` sobre SQLite sin CGO (modernc.org/sqlite, WAL, gob, HMAC-SHA256) + valkey (go-redis/v9 con 3 scripts Lua verbatim).
- **API HTTP completa**: 19 rutas fijas + `/metrics` opcional (`/search` con formatos html/json/csv/rss, `/autocompleter`, `/preferences`, `/stats`, `/config`, `/image_proxy`, `/healthz`, `/opensearch.xml`, `/robots.txt`, etc.) — ver sección Arquitectura.
- **API JSON compatible con SearXNG**: mismo contrato observable (7 campos) y mismo scoring en resultados.
- **i18n**: go-i18n/v2 con locales `en` y `es` (21 claves).
- **Métricas Prometheus**: `client_golang` con contadores de éxito/error/timeout/suspensión por engine y duración de búsqueda.

## Quickstart

**Requisitos**:
- Go ≥ 1.25
- Sin CGO (la caché SQLite usa `modernc.org/sqlite`, implementación pura Go)

**Pasos**:

```sh
cd nanuq-engine
go build ./...
go run ./cmd/nanuq-server -config settings.yml
```

Abrir `http://127.0.0.1:8888` en el navegador.

> **Estado**: el servidor web (`internal/webapp`) está **cableado y operativo end-to-end** (TASK-022 completado). `cmd/nanuq-server/main.go` construye el grafo completo de dependencias — registry de engines (14 módulos), bang store embebido, catálogo de engines, cliente de red, servicio de búsqueda y front-end HTTP, más limiter y métricas opcionales — y arranca `ListenAndServe` en `server.bind_address:server.port`. `http://127.0.0.1:8888/healthz` responde `OK` y `/search` devuelve resultados reales.

## Configuración

La configuración vive en `settings.yml` (ver el ejemplo incluido en el repositorio). Los **defaults son code-first** (DSG-007): todo lo que aparece en YAML solo sobreescribe `newConfig()`.

Secciones principales:

```yaml
general:
  instance_name: "nanuq"

search:
  safe_search: 0
  autocomplete: ""
  ban_time_on_fail: 5
  max_ban_time_on_fail: 120
  formats: [html]

server:
  port: 8888
  bind_address: "127.0.0.1"
  base_url: false
  limiter: false
  public_instance: false
  secret_key: "changeme"
  image_proxy: false

valkey:
  url: false

outgoing:
  request_timeout: 3.0
  pool_connections: 100
  pool_maxsize: 20
  enable_http2: true

engines: []
```

**Variables de entorno** (overrides de configuración):
- `PORT` — puerto del servidor
- `BASE_URL` — URL base pública
- `SECRET_KEY` — clave secreta (firmas/cookies)
- `VALKEY_URL` — URL de conexión a valkey

**Ejemplo de engine data-driven XPath**:

```yaml
engines:
  - name: wikipedia
    engine: xpath
    shortcut: wiki
    categories: general
    search_url: https://en.wikipedia.org/w/index.php?search={query}
    results_xpath: //div[@class="mw-search-result-heading"]/a
```

**Ejemplo de engine data-driven JSON**:

```yaml
engines:
  - name: my_json_api
    engine: json_engine
    categories: general
    search_url: https://api.example.com/search?q={query}
    results_query: results/items
```

## Arquitectura

```
nanuq-engine/
├── cmd/
│   └── nanuq-server/
│       └── main.go            # bootstrap completo (TASK-022): flag -config, config.Load, engines.RegisterAll (14), bang.LoadEmbedded, RegistryCatalog, network, search, webapp, limiter/metrics opcionales, ListenAndServe
├── internal/
│   ├── config/                # structs tipados YAML, defaults code-first, env overrides
│   ├── engine/                # contratos: interfaz Engine, Registry 1:N, EngineSuspendError
│   ├── engines/               # 14 módulos: xpath, json_engine + 12 core (RegisterAll)
│   ├── search/                # parser de query, SearchService, EngineProcessor, ResultContainer
│   ├── result/                # RawResult (13 kinds), Merge, CalculateScore, GetOrderedResults
│   ├── network/               # Client HTTP/2, ShuffleCiphers, RaiseForHTTPError, proxies SOCKS5/5h (SOCKS4 pendiente, REQ-013)
│   ├── webapp/                # ServeMux Go 1.22, 19 rutas fijas + /metrics opcional, templates embed.FS (html/template)
│   ├── autocomplete/          # 5 backends: duckduckgo, google_complete, wikipedia, bing, brave
│   ├── cache/                 # ExpireCache (SQLite sin CGO) + valkey con scripts Lua
│   ├── limiter/               # rate-limit por IP: BURST/LONG/API/SUSPICIOUS
│   ├── i18n/                  # go-i18n/v2, locales en+es
│   └── metrics/               # Prometheus client_golang
└── pkg/
    └── nanuq/                 # fachada pública sin HTTP (DSG-018): Factory, Service, Engine, Result (consumidores MCP)
```

**Flujo de una búsqueda**: la query entra por `/search` → se parsea en `RawTextQuery` (clases Timeout/Language/ExternalBang/Bang/FeelingLucky) → `SearchService` orquesta la cadena `external_bang → answerers → standard` con `errgroup` + watchdog → cada engine pasa por `EngineProcessor` (suspensión con backoff de ban 5→120s) → `network.Client` realiza la petición → los engines producen `RawResult` → `ResultContainer` deduplica por URL y aplica scoring → `GetOrderedResults` ordena → respuesta serializada en JSON o HTML.

**Rutas HTTP (REQ-017)**: `/`, `/search` (html/json/csv/rss), `/autocompleter`, `/preferences`, `/stats`, `/stats/errors`, `/config`, `/engine_descriptions.json`, `/image_proxy`, `/metrics` (opcional: se monta cuando `general.enable_metrics=true`), `/healthz`, `/opensearch.xml`, `/robots.txt`, `/manifest.json`, `/favicon.ico`, `/clear_cookies`, `/about`, `/rss.xsl`, `/client/{token}`, `/logo/{resolution}`.

## Engines portados vs NO portados

**14 módulos registrados** vía `engines.RegisterAll`:

| Módulo | Tipo | Categoría |
|--------|------|-----------|
| `xpath` | data-driven (XPath 1.0) | configurable por YAML |
| `json_engine` | data-driven (JSON path) | configurable por YAML |
| `wikipedia` | core | general |
| `baidu` | core | general |
| `duckduckgo` | core (1:N html/extra/weather) | general |
| `bing` | core | general |
| `bing_images` | core | images |
| `bing_news` | core | news |
| `bing_videos` | core | videos |
| `google` | core | general |
| `brave` | core | general |
| `qwant` | core | general |
| `mojeek` | core | general |
| `startpage` | core | general |

> **NOTA**: los ~230 engines custom de SearXNG **NO están portados** (decisión de alcance — ver `.agents/specs/nanuq-engine/decisions.md`, DECISION-002). Son módulos Python con selectores específicos que se rompen solos cuando los sitios cambian su HTML; portarlos era el 80% del esfuerzo con peor relación beneficio/mantenimiento. Los data-driven cubren ~70-80% de las consultas reales.

## Limitaciones y deuda técnica

1. **Sin botdetection heurístico** — solo rate-limit por IP (`limiter`); no hay análisis de comportamiento de usuario.
2. **TLS fingerprint no browser-like** — `ShuffleCiphers` es un anti-fingerprint básico, no una huella real de navegador.
3. **`vqd` de DuckDuckGo no portado** — requiere caché + red para la lógica de token; pendiente.
4. **Traits de regiones no portados** — los parámetros regionales de los engines no están implementados.
5. **`image_proxy` sin HMAC del parámetro `h`** — el proxy de imágenes no firma la URL todavía.
6. **`startpage/get_sc_code` y EngineCache SQLite no portados** — lógica de token y caché de engines pendiente.
7. **Ventana `SUSPICIOUS` definida pero inactiva** — la ventana de 30d/3 está en el limiter pero no se aplica.

## Licencia y atribución

Inspirado en **SearXNG** (licencia AGPL-3.0). El código portado es una reimplementación independiente (lógica, no código verbatim).

El dataset `internal/bang/data/external_bangs.json` (13,561 bangs) es una copia exacta del dataset de SearXNG (`searx/data/external_bangs.json`), mantenido a partir de las definiciones públicas de bangs de DuckDuckGo.

> **Restricciones de distribución**: ver `internal/bang/data/README.md`. Antes de cualquier distribución pública debe re-verificarse la licencia del dataset (compatibilidad AGPL para archivos de datos, licencia de datos de DuckDuckGo y origen de las entradas). El código consumidor es implementación independiente; solo el archivo de datos está copiado.

## Desarrollo

**Comandos de verificación** (PowerShell en Windows):

```powershell
go build ./...
go test ./...
go vet ./...
$env:CGO_ENABLED='0'; go build ./...
gofmt -l .
```

**Convenciones del proyecto**:
- Logging estructurado con `log/slog` (REQ-NF-007), a stderr.
- Errores envueltos con `%w` para trazabilidad de contexto.
- Renderizado con `html/template` (autoescape siempre activo, REQ-NF-005); prohibido `text/template`.
- Cero reflection: tipos estáticos y dispatch explícito (discriminador `Kind` en resultados).
- Sin CGO: dependencias puras Go (`modernc.org/sqlite`, `x/net`).
