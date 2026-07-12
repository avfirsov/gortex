package mcp

import (
	"context"
	"encoding/json"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func TestFacadeProtocolEnforcesLocalizationFollowupBudget(t *testing.T) {
	srv, _ := setupTestServer(t)
	ctx := WithSessionID(context.Background(), "facade_localization_budget")

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

	var helperID string
	for _, node := range srv.engineFor(ctx).FindSymbols("helper") {
		if node != nil && node.Name == "helper" {
			helperID = node.ID
			break
		}
	}
	require.NotEmpty(t, helperID)

	explored := call(2, "explore", map[string]any{
		"operation": "task",
		"task":      "locate the helper function in main.go",
		"options":   map[string]any{"max_symbols": 1},
	})
	require.False(t, explored.IsError, toolResultText(explored))
	require.Contains(t, toolResultText(explored), helperID)
	exploreControl := localizationControlFromResult(t, explored)
	require.Equal(t, 1, exploreControl.FollowupBudget)

	read := call(3, "read", map[string]any{
		"operation": "source",
		"target":    map[string]any{"symbol": helperID},
	})
	require.False(t, read.IsError, toolResultText(read))
	require.Contains(t, toolResultText(read), "func helper() {}")
	readControl := localizationControlFromResult(t, read)
	require.True(t, readControl.AnswerNow)
	require.Zero(t, readControl.FollowupBudget)

	blocked := call(4, "search", map[string]any{
		"operation": "text",
		"query":     "helper",
	})
	require.False(t, blocked.IsError, toolResultText(blocked))
	require.Contains(t, toolResultText(blocked), "No further search/read/explore work was run")
	require.Contains(t, toolResultText(blocked), helperID)
	blockedControl := localizationControlFromResult(t, blocked)
	require.True(t, blockedControl.AnswerNow)
	require.False(t, blockedControl.Executed)
}
