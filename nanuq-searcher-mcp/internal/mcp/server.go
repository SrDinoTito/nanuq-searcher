package mcp

import (
	"context"
	"log/slog"
	"runtime/debug"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"nanuq-engine/pkg/nanuq"
	"nanuq-searcher-mcp/internal/config"
)

// Server identity advertised during the MCP initialize handshake.
const (
	ServerName    = "nanuq-searcher-mcp"
	serverVersion = "0.1.0"
)

// Server bundles the dependencies shared by the tool handlers: the search
// Service (consumed from TASK-006 on), the MCP-side config (nil until a
// dedicated MCP config section is wired) and the slog logger used by the
// tool-call middleware (NFR-005).
type Server struct {
	svc *nanuq.Service
	cfg *config.Config
	log *slog.Logger
}

// NewServer builds the MCP server for nanuq-searcher-mcp and registers the
// three tools (nanuq_search, nanuq_fetch, nanuq_map). A middleware logs one
// line per tool call with tool, duration_ms and status (NFR-005).
func NewServer(svc *nanuq.Service, cfg *config.Config, log *slog.Logger) *mcpserver.MCPServer {
	s := &Server{svc: svc, cfg: cfg, log: log}

	srv := mcpserver.NewMCPServer(ServerName, serverVersion)
	if log != nil {
		srv.Use(toolLogMiddleware(log))
	}
	srv.AddTool(searchTool(), s.handleSearch)
	srv.AddTool(fetchTool(), s.handleFetch)
	srv.AddTool(mapTool(), s.handleMap)
	return srv
}

// ServeStdio serves srv over stdin/stdout. It blocks until the client closes
// the stream or the process receives SIGINT/SIGTERM.
func ServeStdio(srv *mcpserver.MCPServer) error {
	return mcpserver.ServeStdio(srv)
}

// toolLogMiddleware wraps every tool handler with structured logging
// (NFR-005): tool name, elapsed milliseconds and ok/error status.
//
// It also recovers from handler panics (DSG-011): a buggy handler must never
// crash the whole MCP server. The panic is logged with its stack trace and
// converted into a markdown error result (## ⚠️ Error interno) without
// isError, consistent with how domain errors are rendered (TASK-014).
func toolLogMiddleware(log *slog.Logger) mcpserver.ToolHandlerMiddleware {
	return func(next mcpserver.ToolHandlerFunc) mcpserver.ToolHandlerFunc {
		return func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
			start := time.Now()

			var result *mcpgo.CallToolResult
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Error("tool panic",
							"tool", req.Params.Name,
							"panic", r,
							"stack", string(debug.Stack()),
						)
						result = mcpgo.NewToolResultText("## ⚠️ Error interno\n\nEl handler panico inesperadamente.")
						err = nil
					}
				}()
				result, err = next(ctx, req)
			}()

			status := "ok"
			if err != nil || (result != nil && result.IsError) {
				status = "error"
			}
			log.Info("tool call",
				"tool", req.Params.Name,
				"duration_ms", time.Since(start).Milliseconds(),
				"status", status,
			)
			return result, err
		}
	}
}
