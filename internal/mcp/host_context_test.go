package mcp

import (
	"context"
	"encoding/json"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func TestResolveHostContext(t *testing.T) {
	require.Equal(t, "claude-code", resolveHostContext("claude-code").name)
	require.Equal(t, "claude-code", resolveHostContext("Claude Code 1.4").name)
	require.Equal(t, "cursor", resolveHostContext("Cursor").name)
	require.Equal(t, "vscode", resolveHostContext("Visual Studio Code").name)
	require.Equal(t, "", resolveHostContext("some-unknown-agent").name)
	require.Equal(t, "", resolveHostContext("Claude Desktop").name)
	require.Equal(t, "", resolveHostContext("not-codex").name)
	require.True(t, resolveHostContext("").empty(), "an unidentified host applies no adaptation")
}

// TestHostContext_ApplyExcludesAndOverrides exercises the tool-exclusion
// and description-override knobs with a synthetic context.
func TestHostContext_ApplyExcludesAndOverrides(t *testing.T) {
	hc := hostContext{
		name:         "synthetic",
		excluded:     map[string]bool{"search_symbols": true},
		descOverride: map[string]string{"get_symbol": "OVERRIDDEN"},
	}
	out := hc.apply([]mcplib.Tool{
		{Name: "search_symbols", Description: "x"},
		{Name: "get_symbol", Description: "orig"},
		{Name: "smart_context", Description: "y"},
	})
	got := map[string]string{}
	for _, tl := range out {
		got[tl.Name] = tl.Description
	}
	require.NotContains(t, got, "search_symbols", "an excluded tool must be dropped from the surface")
	require.Equal(t, "OVERRIDDEN", got["get_symbol"], "a description override must apply")
	require.Equal(t, "y", got["smart_context"], "an untouched tool keeps its description")
}

// TestToolProfile_ReportsHostContext confirms the active host context is
// surfaced through tool_profile once the client has identified itself.
func TestToolProfile_ReportsHostContext(t *testing.T) {
	srv, _ := setupTestServer(t)
	srv.session.recordClientName("cursor")

	req := mcplib.CallToolRequest{}
	req.Params.Name = "tool_profile"
	// Pin JSON: recording a client name changes the default wire format.
	req.Params.Arguments = map[string]any{"format": "json"}
	res, err := srv.handleToolProfile(context.Background(), req)
	require.NoError(t, err)
	require.False(t, res.IsError)

	var profile map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &profile))
	require.Equal(t, "cursor", profile["host"], "tool_profile must report the resolved host")
	require.NotContains(t, profile, "host_instruction", "legacy tool_profile must not recommend compact-only names")
}
