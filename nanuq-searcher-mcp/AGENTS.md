# AGENTS.md — nanuq-searcher-mcp

Índice del módulo MCP. El workspace padre (`nanuq-sercher`) contiene el spec SDD
completo en `.agents/specs/nanuq-searcher-mcp/`.

## Skills

Skills de workspace aplicables (cargar con `skill({ name: "..." })`):
- **project-conventions** → estructura AGENTS.md + .agents/, formato DOMAIN.md
- **backend-workspace** → navegación de un proyecto backend Go (framework, layout)
- **codebase-memory-mcp** → indexación/consulta del grafo de código
- **communication-contracts** → formatos de mensajes entre agentes (RESULT/FAILURE)
- **engram-contracts** → contratos de memoria persistente (subagente nunca escribe)

## Specs Activos

- **nanuq-searcher-mcp** (status: active) → `.agents/specs/nanuq-searcher-mcp/`
  del workspace padre. Contiene: `requirements.md`, `design.md`, `tasks.md`,
  `decisions.md`. TASK-015 (QA final + documentación + CI) es el último gate.

## Estructura del módulo

```
nanuq-searcher-mcp/
├── cmd/nanuq-mcp/          → entrypoint: flags, logger, NewServiceFromFile, ServeStdio
├── internal/
│   ├── mcp/                → server MCP: tools.go (defs+handlers), server.go (NewServer, middleware)
│   ├── config/             → Config MCP (env NANUQ_*, validación ozzo), embed.go + settings.default.yml
│   ├── domain/             → modelos de dominio puros (SearchResult, MapSite, FetchResult)
│   ├── search/             → adapter a nanuq-engine + projector dominio
│   ├── fetch/              → cliente HTTP (timeout/size/redirects), extracción de artículo
│   ├── crawl/              → crawler BFS concurrente, robots.txt, normalización de URLs
│   ├── markdown/           → render de dominio a markdown (search.go, map.go, convert.go)
│   └── application/        → capa de aplicación (solo doc.go; handlers viven en internal/mcp)
├── configs/                → settings.default.yml (embebida vía go:embed)
├── go.mod / go.sum
├── .golangci.yml           → linters v2: errcheck/govet/staticcheck/ineffassign/unused
└── README.md               → documentación completa de usuario
```

## Convenciones

- **Handlers como métodos sobre `*Server`** (`internal/mcp`): `handleSearch`,
  `handleFetch`, `handleMap` — acceso a `s.svc`, `s.cfg`, `s.log`.
- **`isError` SOLO para errores de entrada** (validación de argumentos). Los
  errores de dominio (red, engine, timeout) se devuelven como **markdown dentro
  del resultado**, nunca como excepción MCP (NFR-004, DSG-011).
- **Dominio → markdown**: la presentación vive en `internal/markdown/`; los
  handlers de tool solo orquestan y convierten. El dominio no sabe de markdown.
- **`doc.go` solo con el comentario de package** — nada más en esos archivos.
- **NO correr `go mod tidy` hasta el gate final**: la tarea TASK-015 ejecutó el
  tidy final; los cambios de dependencias deben declararse explícitamente en
  go.mod y verificarse con `go build`.
- **stdout SOLO protocolo** (REQ-001): toda salida de logs va a stderr (slog).
- **Config vía env `NANUQ_*`** + defaults de código (DSG-010); validación con
  ozzo-validation en `Config.Validate()`.
- **Tests por paquete** (tabla de cobertura en README): gates de calidad en
  REQ-018 — vet, lint, `-race`, cobertura ≥80% por paquete.

## Dependencias

- **nanuq-engine** → `replace nanuq-engine => ../nanuq-engine` (módulo hermano).
  `nanuq.NewServiceFromFile(cfgPath, getenv)` crea el servicio con la settings
  YAML del engine; las claves `NANUQ_<ENGINE>_API_KEY` se inyectan ahí.
- **mcp-go v0.57.0** — transporte stdio **por líneas** (JSON-RPC, un mensaje por
  línea; NO usa framing Content-Length).
- **html-to-markdown/v2 v2.5.2**, **go-readability/v2 v2.1.2** (fork readeck),
  **robotstxt v1.1.2**, **x/net v0.57.0**, **ozzo-validation/v4 v4.4.1**.
- **.agents/specs/nanuq-searcher-mcp/** (del workspace padre) — requirements,
  design, tasks y decisions del módulo.

## Gates de QA (TASK-015, estado actual: PASS)

- `go build ./...`, `go vet ./...`, `go test ./... -race -count=1` → limpio.
- `go test -cover ./...` → todos los paquetes con código ≥80%.
- `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.1.4 run ./...` → 0 issues
  (el binario local `golangci-lint` es v1; usar el `go run` con v2.1.4).
- `CGO_ENABLED=0 go build -trimpath -o nanuq-mcp.exe ./cmd/nanuq-mcp` → estático (~19 MiB).
- E2E: handshake initialize → tools/list → tools/call para los 3 tools (AC-006).
