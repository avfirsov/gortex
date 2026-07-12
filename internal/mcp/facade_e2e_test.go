package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

// TestFacadeProtocolReadAndGuardedEdit exercises the user-facing named-client
// path through real MCP frames and the real file handlers. It guards two core
// compact-surface promises: read.file can return an uncompressed source file
// immediately, and edit.file preserves the legacy dry-run/cardinality guards.
func TestFacadeProtocolReadAndGuardedEdit(t *testing.T) {
	srv, root := setupTestServer(t)
	ctx := WithSessionID(context.Background(), "facade_real_file_ops")

	initFrame := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"integration-harness","version":"1.0"}}}`)
	require.NotNil(t, srv.MCPServer().HandleMessage(ctx, initFrame))

	call := func(id int, name string, arguments map[string]any) *mcpgo.CallToolResult {
		t.Helper()
		frame, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"method":  "tools/call",
			"params": map[string]any{
				"name":      name,
				"arguments": arguments,
			},
		})
		require.NoError(t, err)
		reply := srv.MCPServer().HandleMessage(ctx, frame)
		require.NotNil(t, reply)
		raw, err := json.Marshal(reply)
		require.NoError(t, err)
		var envelope struct {
			Error  any                   `json:"error"`
			Result *mcpgo.CallToolResult `json:"result"`
		}
		require.NoError(t, json.Unmarshal(raw, &envelope))
		require.Nil(t, envelope.Error)
		require.NotNil(t, envelope.Result)
		return envelope.Result
	}

	readResult := call(2, "read", map[string]any{
		"operation": "file",
		"target":    map[string]any{"file": "main.go"},
		"options":   map[string]any{"compress_bodies": false},
		"output":    map[string]any{"format": "json"},
	})
	require.False(t, readResult.IsError, toolResultText(readResult))
	require.Contains(t, toolResultText(readResult), "func helper() {}",
		"an explicit uncompressed facade read must preserve function bodies")
	require.NotContains(t, toolResultText(readResult), "lines elided")

	path := filepath.Join(root, "main.go")
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	editResult := call(3, "edit", map[string]any{
		"operation":   "file",
		"target":      map[string]any{"file": "main.go"},
		"match":       "func helper() {}",
		"replacement": `func helper() { panic("dry-run") }`,
		"guard":       map[string]any{"expected_occurrences": 1},
		"dry_run":     true,
		"output":      map[string]any{"format": "json"},
	})
	require.False(t, editResult.IsError, toolResultText(editResult))
	preview := unmarshalResult(t, editResult)
	require.Equal(t, true, preview["dry_run"])
	require.Equal(t, float64(1), preview["replacements"])
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after, "a facade dry-run must not modify the file")
}
