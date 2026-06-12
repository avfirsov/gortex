package mcp

import (
	"encoding/json"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/query"
)

// runGraphQuery executes a graph_query call and unmarshals the JSON
// SubGraph result. It fails the test on a tool error.
func runGraphQuery(t *testing.T, srv *Server, q string, limit int) query.SubGraph {
	t.Helper()
	args := map[string]any{"query": q}
	if limit > 0 {
		args["limit"] = limit
	}
	result := callTool(t, srv, "graph_query", args)
	require.False(t, result.IsError, "graph_query errored: %s", toolResultText(result))
	var sg query.SubGraph
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(mcplib.TextContent).Text), &sg))
	return sg
}

func gqNodeIDs(sg query.SubGraph) map[string]bool {
	ids := make(map[string]bool, len(sg.Nodes))
	for _, n := range sg.Nodes {
		ids[n.ID] = true
	}
	return ids
}

// TestGraphQuery_NodesSeed covers the 'nodes' stage with each filter.
func TestGraphQuery_NodesSeed(t *testing.T) {
	srv, _ := setupTestServer(t)

	t.Run("kind filter", func(t *testing.T) {
		sg := runGraphQuery(t, srv, "nodes kind=function", 0)
		require.NotEmpty(t, sg.Nodes)
		for _, n := range sg.Nodes {
			assert.Equal(t, "function", string(n.Kind))
		}
		assert.True(t, gqNodeIDs(sg)["main.go::main"])
		assert.True(t, gqNodeIDs(sg)["main.go::helper"])
	})

	t.Run("name regex filter", func(t *testing.T) {
		sg := runGraphQuery(t, srv, "nodes name~^help", 0)
		ids := gqNodeIDs(sg)
		assert.True(t, ids["main.go::helper"])
		assert.False(t, ids["main.go::main"])
	})

	t.Run("kind type filter", func(t *testing.T) {
		sg := runGraphQuery(t, srv, "nodes kind=type", 0)
		ids := gqNodeIDs(sg)
		assert.True(t, ids["main.go::Config"], "Config struct must be found")
	})

	t.Run("lang filter", func(t *testing.T) {
		sg := runGraphQuery(t, srv, "nodes kind=function lang=go", 0)
		require.NotEmpty(t, sg.Nodes)
		for _, n := range sg.Nodes {
			assert.Equal(t, "go", n.Language)
		}
	})

	t.Run("multiple filters are ANDed", func(t *testing.T) {
		sg := runGraphQuery(t, srv, "nodes kind=function name~^main$", 0)
		ids := gqNodeIDs(sg)
		assert.True(t, ids["main.go::main"])
		assert.False(t, ids["main.go::helper"])
	})
}

// TestGraphQuery_Traverse covers the 'traverse' stage.
func TestGraphQuery_Traverse(t *testing.T) {
	srv, _ := setupTestServer(t)

	t.Run("out reaches callee", func(t *testing.T) {
		sg := runGraphQuery(t, srv, "nodes name~^main$ kind=function | traverse calls out", 0)
		ids := gqNodeIDs(sg)
		assert.True(t, ids["main.go::helper"], "traverse calls out from main must reach helper")
	})

	t.Run("in reaches caller", func(t *testing.T) {
		sg := runGraphQuery(t, srv, "nodes name~^helper$ kind=function | traverse calls in", 0)
		ids := gqNodeIDs(sg)
		assert.True(t, ids["main.go::main"], "traverse calls in from helper must reach main")
	})

	t.Run("default direction is out", func(t *testing.T) {
		sg := runGraphQuery(t, srv, "nodes name~^main$ kind=function | traverse calls", 0)
		ids := gqNodeIDs(sg)
		assert.True(t, ids["main.go::helper"])
	})

	t.Run("traverse replaces working set", func(t *testing.T) {
		// After traverse, the result is the expanded set (helper), not main.
		sg := runGraphQuery(t, srv, "nodes name~^main$ kind=function | traverse calls out", 0)
		ids := gqNodeIDs(sg)
		assert.False(t, ids["main.go::main"], "traverse must replace the working set, not union it")
	})
}

// TestGraphQuery_FilterStage covers the 'filter' stage narrowing.
func TestGraphQuery_FilterStage(t *testing.T) {
	srv, _ := setupTestServer(t)

	sg := runGraphQuery(t, srv, "nodes kind=function | filter name~^main$", 0)
	ids := gqNodeIDs(sg)
	assert.True(t, ids["main.go::main"])
	assert.False(t, ids["main.go::helper"], "filter must drop non-matching nodes")
}

// TestGraphQuery_MultiStage runs a full three-stage pipeline.
func TestGraphQuery_MultiStage(t *testing.T) {
	srv, _ := setupTestServer(t)

	sg := runGraphQuery(t, srv,
		"nodes kind=function | filter name~^main$ | traverse calls out", 0)
	ids := gqNodeIDs(sg)
	assert.True(t, ids["main.go::helper"], "pipeline must end at helper")
}

// TestGraphQuery_Limit enforces the result cap.
func TestGraphQuery_Limit(t *testing.T) {
	srv, _ := setupTestServer(t)

	sg := runGraphQuery(t, srv, "nodes kind=function", 1)
	assert.LessOrEqual(t, len(sg.Nodes), 1, "limit must cap the node set")
}

// TestGraphQuery_Malformed verifies that bad queries return clear errors.
func TestGraphQuery_Malformed(t *testing.T) {
	srv, _ := setupTestServer(t)

	cases := []struct {
		name      string
		query     string
		wantInErr string
	}{
		{"empty string", "", "empty query"},
		{"blank", "   ", "empty query"},
		{"unknown verb", "fetch kind=function", "unknown stage verb"},
		{"traverse first", "traverse calls out", "cannot be the first stage"},
		{"filter first", "filter kind=function", "cannot be the first stage"},
		{"nodes not first", "nodes kind=function | nodes kind=type", "must be the first stage"},
		{"malformed filter", "nodes kindx=function", "malformed filter"},
		{"empty filter value", "nodes kind=", "empty value"},
		{"bad name regex", "nodes name~[unclosed", "invalid name~ regex"},
		{"traverse bad edge kind", "nodes kind=function | traverse bogus out", "unknown edge kind"},
		{"traverse bad direction", "nodes kind=function | traverse calls sideways", "direction must be"},
		{"traverse no edges", "nodes kind=function | traverse", "needs an edge-kind list"},
		{"too many stages", "nodes kind=function | filter kind=function | filter kind=function | filter kind=function | filter kind=function | filter kind=function", "too many stages"},
		{"empty stage", "nodes kind=function | ", "is empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := callTool(t, srv, "graph_query", map[string]any{"query": tc.query})
			require.True(t, result.IsError, "expected an error for query %q", tc.query)
			assert.Contains(t, toolResultText(result), tc.wantInErr)
		})
	}
}

// TestGraphQuery_MissingArg rejects a call with no query argument.
func TestGraphQuery_MissingArg(t *testing.T) {
	srv, _ := setupTestServer(t)
	result := callTool(t, srv, "graph_query", map[string]any{})
	require.True(t, result.IsError)
	assert.Contains(t, toolResultText(result), "query is required")
}

// TestGraphQuery_GCXFormat verifies the GCX wire format is produced.
func TestGraphQuery_GCXFormat(t *testing.T) {
	srv, _ := setupTestServer(t)

	result := callTool(t, srv, "graph_query", map[string]any{
		"query":  "nodes kind=function",
		"format": "gcx",
	})
	require.False(t, result.IsError)
	assert.Contains(t, toolResultText(result), "graph_query.nodes")
}
