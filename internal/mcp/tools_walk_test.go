package mcp

import (
	"encoding/json"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/query"
)

// TestWalkGraph_FollowsCalls verifies walk_graph traverses the call
// graph from a start symbol and returns the reachable callees.
func TestWalkGraph_FollowsCalls(t *testing.T) {
	srv, _ := setupTestServer(t)

	result := callTool(t, srv, "walk_graph", map[string]any{
		"id":         "main.go::main",
		"edge_kinds": "calls",
		"direction":  "out",
	})
	require.False(t, result.IsError, "walk_graph errored: %s", toolResultText(result))

	var sg query.SubGraph
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(mcplib.TextContent).Text), &sg))

	ids := make(map[string]bool)
	for _, n := range sg.Nodes {
		ids[n.ID] = true
	}
	assert.True(t, ids["main.go::main"], "seed must be in the result")
	assert.True(t, ids["main.go::helper"], "callee helper must be reached")
}

// TestWalkGraph_MissingID surfaces a clear error for an absent symbol.
func TestWalkGraph_MissingID(t *testing.T) {
	srv, _ := setupTestServer(t)

	result := callTool(t, srv, "walk_graph", map[string]any{
		"id": "no/such::Symbol",
	})
	require.True(t, result.IsError)
	assert.Contains(t, toolResultText(result), "symbol not found")
}

// TestWalkGraph_RequiresID rejects a call with no id.
func TestWalkGraph_RequiresID(t *testing.T) {
	srv, _ := setupTestServer(t)

	result := callTool(t, srv, "walk_graph", map[string]any{})
	require.True(t, result.IsError)
	assert.Contains(t, toolResultText(result), "id is required")
}

// TestWalkGraph_BadEdgeKind rejects an unknown edge kind.
func TestWalkGraph_BadEdgeKind(t *testing.T) {
	srv, _ := setupTestServer(t)

	result := callTool(t, srv, "walk_graph", map[string]any{
		"id":         "main.go::main",
		"edge_kinds": "bogus_kind",
	})
	require.True(t, result.IsError)
	assert.Contains(t, toolResultText(result), "bogus_kind")
}

// TestWalkGraph_BadDirection rejects an unknown direction.
func TestWalkGraph_BadDirection(t *testing.T) {
	srv, _ := setupTestServer(t)

	result := callTool(t, srv, "walk_graph", map[string]any{
		"id":        "main.go::main",
		"direction": "sideways",
	})
	require.True(t, result.IsError)
	assert.Contains(t, toolResultText(result), "direction")
}

// TestWalkGraph_TokenBudgetHit checks that a tiny token budget truncates
// the walk and surfaces budget_hit on the JSON response.
func TestWalkGraph_TokenBudgetHit(t *testing.T) {
	srv, _ := setupTestServer(t)

	result := callTool(t, srv, "walk_graph", map[string]any{
		"id":           "main.go::main",
		"edge_kinds":   "calls",
		"token_budget": 1,
	})
	require.False(t, result.IsError, "walk_graph errored: %s", toolResultText(result))

	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(mcplib.TextContent).Text), &raw))
	// A 1-token budget cannot admit the callee, so the walk is truncated.
	assert.Equal(t, true, raw["budget_hit"], "budget_hit must be set on a truncated walk")
}

// TestWalkGraph_GCXFormat verifies the GCX wire format is produced and
// carries the node table.
func TestWalkGraph_GCXFormat(t *testing.T) {
	srv, _ := setupTestServer(t)

	result := callTool(t, srv, "walk_graph", map[string]any{
		"id":         "main.go::main",
		"edge_kinds": "calls",
		"format":     "gcx",
	})
	require.False(t, result.IsError)
	text := toolResultText(result)
	assert.Contains(t, text, "walk_graph.nodes", "GCX output must carry the nodes table")
}
