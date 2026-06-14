package mcp

// tools_understand_test.go — handler tests for the export_understand MCP tool
// (L1.1). Builds a real *Server over a small indexed fixture (setupTestServer)
// and drives handleExportUnderstand across the four argument shapes the tool
// contract promises: default (slim, inline), generic=true, granularity=full,
// and output_path (file + stats summary). Also asserts registration (AC1) so
// the HTTP auto-exposure path (same registry) is covered transitively.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// callUnderstand invokes handleExportUnderstand directly with the given args.
func callUnderstand(t *testing.T, srv *Server, args map[string]any) *mcplib.CallToolResult {
	t.Helper()
	req := mcplib.CallToolRequest{}
	req.Params.Name = "export_understand"
	req.Params.Arguments = args
	res, err := srv.handleExportUnderstand(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, res)
	return res
}

// understandText extracts the single text content of a tool result, asserting
// it is not an error result.
func understandText(t *testing.T, res *mcplib.CallToolResult) string {
	t.Helper()
	require.False(t, res.IsError, "tool returned an error result")
	require.NotEmpty(t, res.Content, "tool result has no content")
	tc, ok := res.Content[0].(mcplib.TextContent)
	require.True(t, ok, "first content must be TextContent")
	return tc.Text
}

// TestExportUnderstand_Registered asserts the tool is in the server's live
// tool list (AC1) — which is the same registry the daemon's HTTP
// /v1/tools/{name} dispatches against, so registration auto-exposes HTTP.
func TestExportUnderstand_Registered(t *testing.T) {
	srv, _ := setupTestServer(t)
	tools := srv.mcpServer.ListTools()
	_, ok := tools["export_understand"]
	assert.True(t, ok, "AC1: export_understand must be registered in the MCP tool list")
}

// TestExportUnderstand_InlineSlim drives the default (slim, inline) path and
// asserts the result is a parseable understand-anything@1 envelope (AC2).
func TestExportUnderstand_InlineSlim(t *testing.T) {
	srv, _ := setupTestServer(t)
	res := callUnderstand(t, srv, map[string]any{})
	text := understandText(t, res)

	var ua struct {
		Version string `json:"version"`
		Kind    string `json:"kind"`
		Project struct {
			GitCommitHash *string `json:"gitCommitHash"`
			AnalyzedAt    string  `json:"analyzedAt"`
		} `json:"project"`
		Nodes []map[string]any `json:"nodes"`
		Edges []map[string]any `json:"edges"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &ua), "inline output must be parseable UA JSON")
	assert.Equal(t, "1.0.0", ua.Version, "AC2: understand-anything@1 version")
	assert.Equal(t, "codebase", ua.Kind, "AC2: understand-anything@1 kind")
	// AnalyzedAt is stamped by the Action layer (handler), never empty.
	assert.NotEmpty(t, ua.Project.AnalyzedAt, "handler must stamp analyzedAt (Action layer)")
	// gitCommitHash must be PRESENT (the struct-tag fix), here best-effort "".
	require.NotNil(t, ua.Project.GitCommitHash, "gitCommitHash must be present (not omitted)")
	assert.NotEmpty(t, ua.Nodes, "fixture must yield UA nodes")
	t.Logf("[IMP:9] inline-slim nodes=%d edges=%d", len(ua.Nodes), len(ua.Edges))
}

// TestExportUnderstand_Generic asserts generic=true yields the generic@1
// {nodes, edges} projection (AC4) — no UA envelope keys.
func TestExportUnderstand_Generic(t *testing.T) {
	srv, _ := setupTestServer(t)
	res := callUnderstand(t, srv, map[string]any{"generic": true})
	text := understandText(t, res)

	var gen struct {
		Nodes []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"nodes"`
		Edges []struct {
			Source string `json:"source"`
			Target string `json:"target"`
		} `json:"edges"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &gen), "generic output must be parseable")
	require.NotEmpty(t, gen.Nodes, "AC4: generic@1 must emit nodes")

	// generic@1 has NO understand-anything envelope: no version/kind/project.
	var envelope map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &envelope))
	assert.NotContains(t, envelope, "version", "generic@1 must not carry the UA version envelope")
	assert.NotContains(t, envelope, "project", "generic@1 must not carry the UA project block")

	ids := make(map[string]bool, len(gen.Nodes))
	for _, n := range gen.Nodes {
		assert.NotEmpty(t, n.ID)
		ids[n.ID] = true
	}
	for _, e := range gen.Edges {
		assert.True(t, ids[e.Source], "generic edge source must reference an emitted node")
		assert.True(t, ids[e.Target], "generic edge target must reference an emitted node")
	}
	t.Logf("[IMP:9] generic nodes=%d edges=%d", len(gen.Nodes), len(gen.Edges))
}

// TestExportUnderstand_FullGranularity asserts granularity=full keeps the
// denied/transform kinds (a full export is at least as large as a slim one).
func TestExportUnderstand_FullGranularity(t *testing.T) {
	srv, _ := setupTestServer(t)

	countNodes := func(args map[string]any) int {
		text := understandText(t, callUnderstand(t, srv, args))
		var ua struct {
			Nodes []map[string]any `json:"nodes"`
		}
		require.NoError(t, json.Unmarshal([]byte(text), &ua))
		return len(ua.Nodes)
	}

	slim := countNodes(map[string]any{"granularity": "slim"})
	full := countNodes(map[string]any{"granularity": "full"})
	t.Logf("[IMP:9] granularity slim_nodes=%d full_nodes=%d", slim, full)
	assert.GreaterOrEqual(t, full, slim, "full granularity must keep at least as many nodes as slim")
}

// TestExportUnderstand_OutputPath asserts the output_path path writes a valid
// UA file and returns a stats summary instead of the inline JSON (AC2).
func TestExportUnderstand_OutputPath(t *testing.T) {
	srv, _ := setupTestServer(t)
	out := filepath.Join(t.TempDir(), "kg.json")

	res := callUnderstand(t, srv, map[string]any{"output_path": out})
	summary := understandText(t, res)

	// Stats summary must reference the written path and counts (mirrors
	// handleExportGraph's respondJSONOrTOON map).
	var stats struct {
		OutputPath string `json:"output_path"`
		Nodes      int    `json:"nodes"`
		Edges      int    `json:"edges"`
		Bytes      int    `json:"bytes"`
	}
	require.NoError(t, json.Unmarshal([]byte(summary), &stats), "output_path result must be a stats summary JSON")
	assert.Equal(t, out, stats.OutputPath, "AC2: summary must echo the output_path")
	assert.Positive(t, stats.Bytes, "AC2: bytes written must be positive")

	// The file on disk must be a parseable UA envelope.
	raw, err := os.ReadFile(out)
	require.NoError(t, err, "AC2: output_path must write the file")
	var ua struct {
		Version string `json:"version"`
	}
	require.NoError(t, json.Unmarshal(raw, &ua))
	assert.Equal(t, "1.0.0", ua.Version, "written file must be a valid UA envelope")
	t.Logf("[IMP:9] output_path file=%s bytes=%d nodes=%d", out, stats.Bytes, stats.Nodes)
}
