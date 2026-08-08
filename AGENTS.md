# AGENTS.md — nanuq-sercher (Workspace)

Índice del workspace de búsqueda. Contiene el motor de búsqueda Go, su MCP server, y ejemplos de referencia.

## Módulos

- **nanuq-engine/** — Motor de búsqueda multi-engine en Go (SearXNG-like). Facade pública `pkg/nanuq` (SearchService, Factory, NewServiceFromFile) + `cmd/nanuq-server`.
- **nanuq-searcher-mcp/** — Servidor MCP (stdio) que expone 3 tools con salida markdown limpia: `nanuq_search` (vía engine in-process), `nanuq_fetch` (HTML→markdown), `nanuq_map` (crawl BFS + robots.txt).
- **example/** — Referencias externas: `mcp-searxng` (estilo de MCP server Go) y `searxng` (fuente Python).

## Specs Activos

- **nanuq-engine** (status: done) → `.agents/specs/nanuq-engine/`
- **nanuq-searcher-mcp** (status: active) → `.agents/specs/nanuq-searcher-mcp/`

## Dependencias

- **nanuq-searcher-mcp** → **nanuq-engine**: integración in-process vía `go.mod replace nanuq-engine => ../nanuq-engine`; usa `nanuq.NewServiceFromFile(cfgPath, getenv)` (cambio aditivo en `pkg/nanuq/mcp.go`).

## Indexación CBM

- Proyecto CBM: `nanuq-searcher-mcp` (índice del módulo) · `C-Users-srDino-Proyects-mcps-nanuq-sercher-nanuq-engine` (índice del engine).
