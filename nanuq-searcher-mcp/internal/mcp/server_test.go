package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

// discardLogger returns a slog logger that writes nowhere.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNewServerRegistersExactlyThreeTools(t *testing.T) {
	srv := NewServer(nil, nil, discardLogger())
	tools := srv.ListTools()
	if len(tools) != 3 {
		t.Fatalf("ListTools() = %d tools, want 3", len(tools))
	}
	for _, name := range []string{ToolSearch, ToolFetch, ToolMap} {
		if _, ok := tools[name]; !ok {
			t.Errorf("missing tool %q", name)
		}
	}
	for name := range tools {
		switch name {
		case ToolSearch, ToolFetch, ToolMap:
		default:
			t.Errorf("unexpected tool %q", name)
		}
	}
}

// TestAllHandlersWired verifies that all three tools carry a real handler
// (REQ-002, REQ-007, REQ-011). This replaces the old stub-handler test:
// with TASK-013, nanuq_search (TASK-006), nanuq_fetch (TASK-010) and
// nanuq_map (TASK-013) are all implemented, so there are no stubs left.
// The behavioral coverage of each handler lives in its own _test.go file.
func TestAllHandlersWired(t *testing.T) {
	srv := NewServer(nil, nil, discardLogger())
	for _, name := range []string{ToolSearch, ToolFetch, ToolMap} {
		st := srv.GetTool(name)
		if st == nil {
			t.Fatalf("tool %q not registered", name)
		}
		if st.Handler == nil {
			t.Errorf("tool %q has no handler wired", name)
		}
	}
}

// TestToolLogMiddlewareRecoversPanic verifies the tool dispatch recover
// (DSG-011 / TASK-014): a panicking handler must never crash the MCP server.
// The panic is logged with its stack trace and converted into a markdown
// "## ⚠️ Error interno" result WITHOUT isError — consistent with how domain
// errors are rendered (the client still gets a readable message).
func TestToolLogMiddlewareRecoversPanic(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	srv := NewServer(nil, nil, log)
	srv.AddTool(
		mcpgo.NewTool("panic_tool"),
		func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
			panic("boom")
		},
	)

	// Dispatch through the real server path: toolLogMiddleware is applied at
	// call time inside HandleMessage, so calling st.Handler directly would
	// bypass the recover.
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "panic_tool",
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	msg := srv.HandleMessage(context.Background(), raw)

	// The server must respond (not crash) with a plain markdown result.
	resp, ok := msg.(mcpgo.JSONRPCResponse)
	if !ok {
		t.Fatalf("response type = %T, want mcp.JSONRPCResponse", msg)
	}
	result, ok := resp.Result.(*mcpgo.CallToolResult)
	if !ok {
		t.Fatalf("Result type = %T, want *mcp.CallToolResult", resp.Result)
	}
	if result.IsError {
		t.Error("recovered panic result has isError set, want plain markdown text")
	}
	if len(result.Content) != 1 {
		t.Fatalf("len(Content) = %d, want 1", len(result.Content))
	}
	txt, ok := result.Content[0].(mcpgo.TextContent)
	if !ok {
		t.Fatalf("Content[0] type = %T, want mcp.TextContent", result.Content[0])
	}
	if !strings.Contains(txt.Text, "## ⚠️ Error interno") {
		t.Errorf("result text = %q, want markdown error header", txt.Text)
	}

	// The panic must be logged with the stack trace.
	if !strings.Contains(buf.String(), "tool panic") {
		t.Errorf("log output = %q, want 'tool panic' entry with stack", buf.String())
	}
}
