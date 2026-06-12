package mcp

import (
	"encoding/json"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

// addOrphanSymbol injects a function node with no edges of any kind
// into the server's graph, mimicking a symbol the extractor never
// processed. Any usage / caller / impact query on it comes back empty.
func addOrphanSymbol(srv *Server, id string) {
	srv.graph.AddNode(&graph.Node{
		ID: id, Kind: graph.KindFunction, Name: id, FilePath: "orphan.go",
	})
}

// decodeJSONResult unmarshals a tool's JSON text content into a map.
func decodeJSONResult(t *testing.T, res *mcplib.CallToolResult) map[string]any {
	t.Helper()
	require.False(t, res.IsError)
	tc, ok := res.Content[0].(mcplib.TextContent)
	require.True(t, ok)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &m))
	return m
}

// TestFindUsages_ZeroEdgeCaveat asserts find_usages attaches the
// extraction-gap caveat when a symbol has no usages and omits it when
// the symbol is used.
func TestFindUsages_ZeroEdgeCaveat(t *testing.T) {
	srv, _ := setupTestServer(t)
	addOrphanSymbol(srv, "orphan.go::Orphan")

	// Zero-edge symbol — the caveat must be present and classify the
	// empty result as an extraction gap.
	empty := decodeJSONResult(t, callTool(t, srv, "find_usages", map[string]any{
		"id": "orphan.go::Orphan",
	}))
	caveat, ok := empty["caveat"].(map[string]any)
	require.True(t, ok, "find_usages on a zero-edge symbol must carry a caveat")
	assert.Equal(t, string(graph.ZeroEdgePossibleExtractionGap), caveat["class"])
	assert.NotEmpty(t, caveat["message"])

	// `helper` is called by `main` — a non-empty result carries no caveat.
	used := decodeJSONResult(t, callTool(t, srv, "find_usages", map[string]any{
		"id": "main.go::helper",
	}))
	edges, _ := used["edges"].([]any)
	require.NotEmpty(t, edges, "fixture: helper must have usages")
	_, hasCaveat := used["caveat"]
	assert.False(t, hasCaveat, "a non-empty find_usages result must omit the caveat")
}

// TestGetCallers_ZeroEdgeCaveat asserts get_callers attaches the
// caveat when a symbol has no callers and omits it when it does.
func TestGetCallers_ZeroEdgeCaveat(t *testing.T) {
	srv, _ := setupTestServer(t)
	addOrphanSymbol(srv, "orphan.go::Orphan")

	empty := decodeJSONResult(t, callTool(t, srv, "get_callers", map[string]any{
		"id": "orphan.go::Orphan",
	}))
	caveat, ok := empty["caveat"].(map[string]any)
	require.True(t, ok, "get_callers on a zero-edge symbol must carry a caveat")
	assert.Equal(t, string(graph.ZeroEdgePossibleExtractionGap), caveat["class"])
	assert.NotEmpty(t, caveat["message"])

	used := decodeJSONResult(t, callTool(t, srv, "get_callers", map[string]any{
		"id": "main.go::helper",
	}))
	edges, _ := used["edges"].([]any)
	require.NotEmpty(t, edges, "fixture: helper must have callers")
	_, hasCaveat := used["caveat"]
	assert.False(t, hasCaveat, "a non-empty get_callers result must omit the caveat")
}

// TestExplainChangeImpact_ZeroEdgeCaveat asserts explain_change_impact
// attaches a per-symbol caveat when the blast radius is empty and omits
// it when the change has affected symbols.
func TestExplainChangeImpact_ZeroEdgeCaveat(t *testing.T) {
	srv, _ := setupTestServer(t)
	addOrphanSymbol(srv, "orphan.go::Orphan")

	empty := decodeJSONResult(t, callTool(t, srv, "explain_change_impact", map[string]any{
		"ids": "orphan.go::Orphan",
	}))
	require.EqualValues(t, 0, empty["total_affected"])
	caveats, ok := empty["zero_impact_caveat"].([]any)
	require.True(t, ok, "explain_change_impact on a zero-edge symbol must carry a caveat")
	require.Len(t, caveats, 1)
	first, _ := caveats[0].(map[string]any)
	assert.Equal(t, "orphan.go::Orphan", first["id"])
	assert.Equal(t, string(graph.ZeroEdgePossibleExtractionGap), first["class"])
	assert.NotEmpty(t, first["message"])

	// `helper` is called by `main` — a non-empty blast radius carries
	// no caveat.
	used := decodeJSONResult(t, callTool(t, srv, "explain_change_impact", map[string]any{
		"ids": "main.go::helper",
	}))
	total, _ := used["total_affected"].(float64)
	require.Greater(t, total, 0.0, "fixture: changing helper must affect main")
	_, hasCaveat := used["zero_impact_caveat"]
	assert.False(t, hasCaveat, "a non-empty explain_change_impact result must omit the caveat")
}

// TestZeroEdgeCaveat_LikelyUnusedClassification asserts the caveat
// distinguishes a genuinely unused symbol (indexed, no callers) from an
// extraction gap. A symbol the indexer saw carries a structural edge,
// so its empty usage query is classified likely_unused, not a gap.
func TestZeroEdgeCaveat_LikelyUnusedClassification(t *testing.T) {
	srv, _ := setupTestServer(t)

	// `Config` is a real indexed type — the file defines it — but
	// nothing references it in the fixture source.
	res := decodeJSONResult(t, callTool(t, srv, "find_usages", map[string]any{
		"id": "main.go::Config",
	}))
	edges, _ := res["edges"].([]any)
	require.Empty(t, edges, "fixture: Config is unreferenced")
	caveat, ok := res["caveat"].(map[string]any)
	require.True(t, ok, "an unreferenced indexed symbol must still carry a caveat")
	assert.Equal(t, string(graph.ZeroEdgeLikelyUnused), caveat["class"],
		"an indexed-but-unused symbol must be likely_unused, not an extraction gap")
}
