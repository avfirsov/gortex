package mcp

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func TestIsToolEnabled(t *testing.T) {
	srv := newFullTestServer(t)
	// search_symbols is a hot eager tool — always live.
	if !srv.IsToolEnabled("search_symbols") {
		t.Error("search_symbols should be enabled")
	}
	// A deferred tool is still reachable (behind tools_search).
	if !srv.IsToolEnabled("store_memory") {
		t.Error("store_memory should be enabled (deferred is still reachable)")
	}
	if srv.IsToolEnabled("definitely_not_a_tool_xyz") {
		t.Error("an unregistered name must report not enabled")
	}
	if srv.IsToolEnabled("") {
		t.Error("empty tool name must report not enabled")
	}
}

func TestToolProfile_FullProfile(t *testing.T) {
	srv := newFullTestServer(t)
	res := callHandler(t, srv.handleToolProfile, map[string]any{})
	out := unmarshalResult(t, res)

	total, _ := out["total"].(float64)
	if total < 50 {
		t.Errorf("total tools = %v, want a substantial surface (>=50)", total)
	}
	live, ok := out["live"].([]any)
	if !ok || len(live) == 0 {
		t.Fatalf("live list missing or empty: %#v", out["live"])
	}
	liveCount, _ := out["live_count"].(float64)
	if int(liveCount) != len(live) {
		t.Errorf("live_count %v disagrees with live list length %d", liveCount, len(live))
	}
	if _, ok := out["scopes"].(map[string]any); !ok {
		t.Errorf("scopes map missing: %#v", out["scopes"])
	}
}

func TestToolProfile_LazyDisabledMakesEverythingLive(t *testing.T) {
	t.Setenv("GORTEX_LAZY_TOOLS", "0")
	srv := newFullTestServer(t)
	res := callHandler(t, srv.handleToolProfile, map[string]any{})
	out := unmarshalResult(t, res)

	if le, _ := out["lazy_enabled"].(bool); le {
		t.Error("lazy_enabled should be false with GORTEX_LAZY_TOOLS=0")
	}
	if dc, _ := out["deferred_count"].(float64); dc != 0 {
		t.Errorf("deferred_count = %v, want 0 when lazy is disabled", dc)
	}
}

func TestToolProfile_PerTool(t *testing.T) {
	srv := newFullTestServer(t)

	res := callHandler(t, srv.handleToolProfile, map[string]any{"tool": "search_symbols"})
	out := unmarshalResult(t, res)
	if en, _ := out["enabled"].(bool); !en {
		t.Error("search_symbols should report enabled")
	}
	if out["status"] != "live" {
		t.Errorf("search_symbols status = %v, want live", out["status"])
	}

	res = callHandler(t, srv.handleToolProfile, map[string]any{"tool": "no_such_tool_xyz"})
	out = unmarshalResult(t, res)
	if en, _ := out["enabled"].(bool); en {
		t.Error("no_such_tool_xyz should report not enabled")
	}
	if out["status"] != "absent" {
		t.Errorf("unknown tool status = %v, want absent", out["status"])
	}
}

// TestToolProfile_CodexSessionMatchesToolsList is the protocol regression for
// the Codex discovery failure: initialize.clientInfo must select facade-v1,
// tools/list must carry the static read facade without promotion, and profile
// introspection must describe that same session rather than global core.
func TestToolProfile_CodexSessionMatchesToolsList(t *testing.T) {
	srv := setupPresetServer(t, ToolPolicyConfig{Preset: "core", Mode: "defer"})
	ctx := WithSessionID(context.Background(), "sess_codex")

	initFrame := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"codex","version":"1.0"}}}`)
	require.NotNil(t, srv.MCPServer().HandleMessage(ctx, initFrame))

	listed := listToolNamesForSession(t, srv, "sess_codex")
	require.True(t, listed["read"], "Codex must receive an eager source-read facade")
	require.False(t, listed["read_file"], "Codex facade-v1 must not leak legacy reader names")
	require.True(t, listed["analyze"], "Codex must receive the complete static facade surface")

	wantLive := append([]string{}, facadePresetTools...)
	sort.Strings(wantLive)
	gotLive := make([]string, 0, len(listed))
	for name := range listed {
		gotLive = append(gotLive, name)
	}
	sort.Strings(gotLive)
	require.Equal(t, wantLive, gotLive, "initialize(codex) must publish the exact eager agent roster")

	req := mcp.CallToolRequest{}
	req.Params.Name = "tool_profile"
	req.Params.Arguments = map[string]any{"format": "json"}
	res, err := srv.handleToolProfile(ctx, req)
	require.NoError(t, err)
	require.False(t, res.IsError)

	var profile map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &profile))
	require.Equal(t, FacadeSurfaceVersion, profile["preset"])
	require.Equal(t, "hide", profile["preset_mode"])
	require.Equal(t, float64(len(wantLive)), profile["live_count"])
	require.Equal(t, stringsToAny(wantLive), profile["live"],
		"tool_profile.live must exactly match this session's tools/list")

	readReq := req
	readReq.Params.Arguments = map[string]any{"format": "json", "tool": "read"}
	readRes, err := srv.handleToolProfile(ctx, readReq)
	require.NoError(t, err)
	readProfile := unmarshalResult(t, readRes)
	require.Equal(t, "live", readProfile["status"])
	require.Equal(t, true, readProfile["enabled"])

	legacyReadReq := req
	legacyReadReq.Params.Arguments = map[string]any{"format": "json", "tool": "read_file"}
	analyzeRes, err := srv.handleToolProfile(ctx, legacyReadReq)
	require.NoError(t, err)
	analyzeProfile := unmarshalResult(t, analyzeRes)
	require.Equal(t, "blocked", analyzeProfile["status"])
	require.Equal(t, false, analyzeProfile["enabled"])
}

func TestToolProfile_SessionHidePolicyReportsBlocked(t *testing.T) {
	srv := setupPresetServer(t, ToolPolicyConfig{Preset: "core", Mode: "defer"})
	srv.NoteSessionToolPolicy("sess_nav", "nav", "hide")
	navCtx := WithSessionID(context.Background(), "sess_nav")

	req := mcp.CallToolRequest{}
	req.Params.Name = "tool_profile"
	req.Params.Arguments = map[string]any{"format": "json", "tool": "edit_file"}
	res, err := srv.handleToolProfile(navCtx, req)
	require.NoError(t, err)
	tool := unmarshalResult(t, res)
	require.Equal(t, "blocked", tool["status"])
	require.Equal(t, false, tool["enabled"])

	req.Params.Arguments = map[string]any{"format": "json"}
	res, err = srv.handleToolProfile(navCtx, req)
	require.NoError(t, err)
	profile := unmarshalResult(t, res)
	require.Equal(t, "nav", profile["preset"])
	require.Equal(t, "hide", profile["preset_mode"])
	require.Contains(t, profile["blocked"], "edit_file")
	require.Equal(t, float64(len(listToolNamesForSession(t, srv, "sess_nav"))), profile["live_count"])

	// A restrictive policy on one connection must not leak into another.
	defaultCtx := WithSessionID(context.Background(), "sess_default")
	req.Params.Arguments = map[string]any{"format": "json", "tool": "edit_file"}
	res, err = srv.handleToolProfile(defaultCtx, req)
	require.NoError(t, err)
	defaultTool := unmarshalResult(t, res)
	require.Equal(t, "live", defaultTool["status"])
	require.Equal(t, true, defaultTool["enabled"])
}

func stringsToAny(in []string) []any {
	out := make([]any, len(in))
	for i, value := range in {
		out[i] = value
	}
	return out
}
