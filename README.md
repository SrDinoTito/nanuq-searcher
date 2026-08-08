# nanuq-searcher

> Motor de búsqueda multi-engine (Go) + servidor MCP con salida limpia en Markdown para agentes de IA.
> Workspace local: `nanuq-sercher`.

Creado por **SrDinoTito** · Licencia: Apache 2.0

## ¿Qué es esto?

Un workspace con módulos Go que funcionan juntos:

| Módulo | Rol |
|---|---|
| `nanuq-engine/` | Motor de búsqueda multi-engine en Go (estilo SearXNG). Facade pública `pkg/nanuq` (`NewServiceFromFile`, `Search`, `Factory`). 14 motores: DuckDuckGo, Wikipedia, Brave, Bing, Google, Qwant, Mojeek, Startpage, Baidu, etc. |
| `nanuq-searcher-mcp/` | Servidor MCP (Model Context Protocol) por stdio que expone el motor como 3 herramientas para agentes de IA, con salida Markdown limpia (sin basura). |
| `example/` | Referencias externas: `mcp-searxng` (estilo de MCP server Go) y `searxng` (fuente Python). |

## Las 3 herramientas del MCP

1. **`nanuq_search`** — Busca en la web con los motores en paralelo (in-process, sin HTTP extra). Devuelve resultados en Markdown: `1. [Título](url) — snippet` + engines/score.
2. **`nanuq_fetch`** — Descarga una página, extrae el contenido legible (go-readability) y lo convierte a Markdown GFM. Modos: `readable` (por defecto) y `full`.
3. **`nanuq_map`** — Recorre un sitio completo (crawler BFS con worker pool), respeta `robots.txt` y devuelve un mapa con árbol de páginas + outline de headings.

## Cómo funciona

```
Agente de IA (Claude, Cursor, etc.)
  │  MCP stdio (JSON-RPC por línea)
  ▼
nanuq-searcher-mcp (server MCP Go)
  ├─ nanuq_search → nanuq-engine pkg/nanuq (in-process, go.mod replace)
  ├─ nanuq_fetch  → HTTP client + go-readability + html-to-markdown
  └─ nanuq_map    → crawler BFS + robots.txt (fail-open) + worker pool
```

- El MCP usa `nanuq-engine` como **librería in-process** (`go.mod replace nanuq-engine => ../nanuq-engine`), sin servidor HTTP intermedio.
- Salida siempre **Markdown limpio**: sin JSON basura, sin parsed_url/template/thumbnails.
- Errores de dominio → Markdown `## ⚠️ ...` (sin romper el protocolo); inputs inválidos → `isError`.

## Requisitos

- Go **1.25.5+**
- Red (para búsqueda/fetch/map)
- Opcional: API keys para los motores que las requieren

## Ponerlo a funcionar

### 1. Compilar el MCP

```bash
cd nanuq-searcher-mcp
go build ./...
```

### 2. Ejecutar (MCP server stdio)

```bash
cd nanuq-searcher-mcp
go run ./cmd/nanuq-mcp
```

> El server queda a la espera de mensajes JSON-RPC por stdio (un JSON por línea).
> Sin `-config` usa una configuración embebida con los 14 motores habilitados.

### 3. Conectarlo a un cliente MCP

- **Claude Desktop / Claude Code**: añadir un MCP server tipo `stdio` apuntando al binario compilado (o `go run ./cmd/nanuq-mcp`).
- **Cursor / otros clientes**: ídem, server `stdio` con el comando del binario.

### 4. Configuración (env vars)

| Variable | Default | Descripción |
|---|---|---|
| `NANUQ_SEARCH_MAX_RESULTS` | 10 | Resultados máximos por búsqueda (1..50) |
| `NANUQ_FETCH_TIMEOUT` | 30 | Timeout del fetch (segundos) |
| `NANUQ_FETCH_MAX_BYTES` | 2097152 | Límite de bytes por página (64KB..10MB) |
| `NANUQ_FETCH_MAX_REDIRECTS` | 5 | Redirects máximos |
| `NANUQ_CRAWL_WORKERS` | 8 | Workers del crawler |
| `NANUQ_CRAWL_MAX_PAGES` | 100 | Páginas máximas por mapa |
| `NANUQ_CRAWL_MAX_DEPTH` | 3 | Profundidad máxima |
| `NANUQ_CRAWL_TIMEOUT` | 15 | Timeout total del crawler (seg) |
| `NANUQ_UA` | `nanuq-mcp/0.1` | User-Agent |
| `NANUQ_BRAVE_API_KEY` | — | API key de Brave |
| `NANUQ_BING_API_KEY` | — | API key de Bing |
| `NANUQ_GOOGLE_API_KEY` | — | API key de Google |

### 5. Probar manualmente (JSON-RPC por línea)

```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0.0.1"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"nanuq_search","arguments":{"query":"golang concurrency"}}}
```

## Ejemplo de salida

```markdown
## Resultados para "golang concurrency"

1. [Go by Example](https://gobyexample.com/) — Go by Example Go is an open source...
   - engines: bing · score: 0.000

## ⚠️ Sin respuesta: bitbucket, wikipedia, mojeek, qwant, startpage, baidu
```

## Calidad

- Cobertura ≥80% por paquete · `go vet` limpio · golangci-lint 0 issues · `go test -race` ALL PASS
- Build estático: `CGO_ENABLED=0 go build -trimpath` → ~19 MiB

## Especs (SDD)

- `nanuq-engine` (done) → `.agents/specs/nanuq-engine/`
- `nanuq-searcher-mcp` (active) → `.agents/specs/nanuq-searcher-mcp/`

## Licencia

[Apache License 2.0](LICENSE) — Copyright © 2026 SrDinoTito.
