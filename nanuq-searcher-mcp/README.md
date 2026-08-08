# nanuq-searcher-mcp

Servidor **MCP** (Model Context Protocol) que expone `nanuq-engine` como herramientas
de búsqueda, extracción y mapeo de sitios web. Comunicación: **stdio** (stdout
solo protocolo, logs a stderr).

> Estado: **completo** — gates de QA final pasados (REQ-018) y E2E verificado con
> red real (AC-006). Ver `spec` en `.agents/specs/nanuq-searcher-mcp/` del
> workspace padre.

---

## Herramientas

### `nanuq_search`
Busca en el índice multi-engine y devuelve los resultados más relevantes con su
contexto extraído (formato markdown).

| Argumento     | Tipo       | Default | Descripción |
|---------------|------------|---------|-------------|
| `query`       | `string`   | —       | **Requerido.** Consulta de búsqueda en texto libre. |
| `categories`  | `string[]` | —       | Filtra resultados a estas categorías de motor (opcional). |
| `max_results` | `int`      | `10`    | Número máximo de resultados (1–50). |

Salida: encabezado con la consulta, lista numerada `1. [Título](url) — snippet`
con `engines`/`score` inline, y sección `⚠️ Sin respuesta` para engines que no
respondieron. Los errores de dominio se devuelven como markdown informativo
(no como error MCP).

### `nanuq_fetch`
Obtiene una URL y la convierte a markdown limpio.

| Argumento   | Tipo     | Default      | Descripción |
|-------------|----------|--------------|-------------|
| `url`       | `string` | —            | **Requerido.** URL absoluta (`http`/`https`). |
| `mode`      | `string` | `"readable"` | `readable` extrae el artículo principal; `full` devuelve el contenido completo. |
| `max_bytes` | `int`    | `2097152`    | Límite de bytes a descargar (64 KB – 10 MB). |

Guardarraíles (REQ-010): solo `http`/`https`, timeout 30s, máx. 5 redirects,
validación de `Content-Type` (rechaza JSON/PDF/XML), detección de charset y
truncado visible al superar `max_bytes`.

### `nanuq_map`
Explora un sitio respetando `robots.txt` y devuelve un mapa de su estructura.

| Argumento        | Tipo      | Default  | Descripción |
|------------------|-----------|----------|-------------|
| `url`            | `string`  | —        | **Requerido.** URL raíz (`http`/`https`) desde la que mapear. |
| `max_pages`      | `int`     | `100`    | Número máximo de páginas a explorar (1–10000). |
| `max_depth`      | `int`     | `3`      | Profundidad máxima de enlaces a seguir (1–10). |
| `same_host`      | `bool`    | `true`   | Limita el mapeo al mismo host que la URL raíz. |
| `respect_robots` | `bool`    | `true`   | Respeta las reglas de `robots.txt` (Disallow/Allow/Crawl-delay). |
| `include_content`| `bool`    | `false`  | Incluye el contenido extraído de cada página. |

Salida: encabezado (URL raíz, visitas, errores), árbol indentado por
profundidad `- [título](url)` y outline H1/H2/H3 por página. La cancelación
devuelve resultado parcial con nota `cancelado: N páginas visitadas`.

---

## Instalación y build

Requiere **Go 1.25.5+** (desarrollado y probado con go1.26.3).

```sh
# desde el directorio del módulo
go build -o nanuq-mcp.exe ./cmd/nanuq-mcp
```

Build estático (NFR-002) — binario único, sin dependencias del sistema:

```sh
CGO_ENABLED=0 go build -trimpath -o nanuq-mcp.exe ./cmd/nanuq-mcp
```

Tamaño típico del binario estático: ~19 MiB (incluye stdlib, mcp-go,
parseadores HTML y extractor de legibilidad).

El módulo depende de `nanuq-engine` vía `replace` local:

```
replace nanuq-engine => ../nanuq-engine
```

**Nota**: no se ha inicializado repositorio git en este módulo (aún). El
workspace padre `nanuq-sercher` lo gestionará cuando se versionen los módulos.

---

## Configuración

La configuración MCP se lee de variables de entorno con prefijo `NANUQ_`.
Los valores ausentes usan los defaults del código.

| Variable                  | Default   | Descripción |
|---------------------------|-----------|-------------|
| `NANUQ_FETCH_TIMEOUT`     | `30`      | Timeout por request de fetch (segundos). |
| `NANUQ_FETCH_MAX_BYTES`   | `2097152` | Límite de bytes a descargar (64 KB – 10 MB). |
| `NANUQ_FETCH_MAX_REDIRECTS`| `5`      | Máx. redirects a seguir. |
| `NANUQ_CRAWL_WORKERS`     | `8`       | Workers concurrentes del crawler (1–64). |
| `NANUQ_CRAWL_MAX_PAGES`   | `100`     | Máx. páginas por mapa (1–10000). |
| `NANUQ_CRAWL_MAX_DEPTH`   | `3`       | Profundidad máx. del mapa (1–10). |
| `NANUQ_CRAWL_TIMEOUT`     | `15`      | Timeout por página durante el crawl (segundos). |
| `NANUQ_SEARCH_MAX_RESULTS`| `10`      | Máx. resultados por búsqueda (1–50). |
| `NANUQ_UA`                | `nanuq-mcp/0.1 (+https://github.com/srDino/nanuq-sercher)` | User-Agent identificable. |

### Claves de API del engine

Los engines que requieren API key se activan con `NANUQ_<ENGINE>_API_KEY`:

| Variable                | Engine  |
|-------------------------|---------|
| `NANUQ_BRAVE_API_KEY`   | Brave   |
| `NANUQ_BING_API_KEY`    | Bing    |
| `NANUQ_GOOGLE_API_KEY`  | Google  |

Los engines sin API key (duckduckgo, rawweb, startpage, qwant, mojeek, baidu,
wikipedia, bitbucket, bing_images/news/videos) funcionan sin configuración.

### Configuración del engine (settings)

El engine usa `configs/settings.default.yml` embebida (14 engines, sin claves).
Se puede sobrescribir con el flag `-config`:

```sh
nanuq-mcp.exe -config /ruta/a/settings.yml
```

La inyección de claves (`NANUQ_<ENGINE>_API_KEY`) ocurre dentro de
`NewServiceFromFile`. Al arrancar se loguean los engines habilitados por
categoría (REQ-017).

---

## Uso con cliente MCP (stdio)

El server habla **JSON-RPC por líneas** sobre stdio (un mensaje por línea, sin
framing `Content-Length`).

### Claude Desktop / clientes MCP compatibles

```json
{
  "mcpServers": {
    "nanuq": {
      "command": "C:\\ruta\\a\\nanuq-mcp.exe",
      "args": [],
      "env": {
        "NANUQ_BRAVE_API_KEY": "tu-clave"
      }
    }
  }
}
```

### Verificación manual (handshake)

```powershell
$msgs = @(
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"cli","version":"0.1.0"}}}',
  '{"jsonrpc":"2.0","method":"notifications/initialized"}',
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
)
$msgs | & .\nanuq-mcp.exe
```

La respuesta de `tools/list` expone las 3 herramientas con sus `inputSchema`
(REQ-002/008/011). Después, `tools/call` con el nombre y `arguments`.

### Ejemplo de salida

**nanuq_search** (`query: "golang concurrency", max_results: 3`):

```markdown
## Resultados para "golang concurrency"

1. [Goroutines in Go: A Practical Guide to Concurrency](https://getstream.io/blog/goroutines-go-concurrency-guide/) — That's why concurrency is so important...
   - engines: duckduckgo · score: 0.000

## ⚠️ Sin respuesta: wikipedia, bitbucket, mojeek, qwant, startpage, baidu
```

**nanuq_fetch** (`url: "https://example.com", mode: "full"`):

```markdown
# https://example.com

URL: https://example.com

# Example Domain

This domain is for use in documentation examples...
```

**nanuq_map** (`url: "https://example.com", max_pages: 3, max_depth: 1`):

```markdown
# Mapa de https://example.com

Visitas: 1 · Cancelado: no

- [Example Domain](https://example.com)
### Example Domain
```

---

## Estado de calidad

Gates de QA final (REQ-018 / AC-005), todos **PASS**:

| Gate                    | Resultado |
|-------------------------|-----------|
| `go build ./...`        | ✅ limpio |
| `go vet ./...`          | ✅ limpio |
| `go test ./... -race -count=1` | ✅ ALL PASS |
| `golangci-lint v2.1.4 run ./...` | ✅ 0 issues |
| `go mod tidy`           | ✅ go.mod limpio (deps directas correctas) |
| Build estático CGO_ENABLED=0 | ✅ ~19 MiB |

Cobertura por paquete (`go test -cover ./...`):

| Paquete              | Cobertura |
|----------------------|-----------|
| `internal/search`    | 98.1%     |
| `internal/markdown`  | 96.3%     |
| `internal/config`    | 93.3%     |
| `internal/mcp`       | 89.7%     |
| `internal/fetch`     | 89.9%     |
| `internal/crawl`     | 87.5%     |
| `internal/domain`    | datos puros (sin statements) |

**Nota E2E**: verificado con red real (AC-006): handshake `initialize` →
`tools/list` → `tools/call` responde para los 3 tools sin crash. Búsqueda real
con engines duckduckgo/rawweb/bing; fetch y map sobre `https://example.com`.

---

## Estructura del módulo

```
cmd/nanuq-mcp/          → entrypoint (config, logger, ServeStdio)
internal/application/   → capa de aplicación (doc.go; handlers en internal/mcp)
internal/mcp/           → server MCP + 3 tool handlers (métodos sobre *Server)
internal/config/        → Config MCP (env NANUQ_*, validación ozzo) + settings embebida
internal/search/        → adapter a nanuq-engine + proyección a modelo de dominio
internal/fetch/         → cliente HTTP con guardarraíles + extracción de artículo
internal/crawl/         → crawler BFS concurrente + robots.txt + normalización
internal/markdown/      → render de resultados de dominio a markdown
internal/domain/        → modelos de dominio puros
configs/                → settings.default.yml (fuente embebida)
```

Ver también: `AGENTS.md` (índice del módulo) y el spec SDD en
`.agents/specs/nanuq-searcher-mcp/` del workspace padre
(`requirements.md`, `design.md`, `tasks.md`, `decisions.md`).
