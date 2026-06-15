package mcp

import (
	"context"
	"encoding/json"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestEnclosingForRange(t *testing.T) {
	// A file with two top-level functions and a closure nested in the first.
	idx := &fileSymbolIndex{}
	idx.add(&graph.Node{ID: "f.go::Outer", Name: "Outer", Kind: graph.KindFunction, StartLine: 1, EndLine: 20})
	idx.add(&graph.Node{ID: "f.go::Outer.closure", Name: "closure", Kind: graph.KindClosure, StartLine: 5, EndLine: 9})
	idx.add(&graph.Node{ID: "f.go::Second", Name: "Second", Kind: graph.KindFunction, StartLine: 22, EndLine: 30})
	idx.finalise()

	// A range inside the closure resolves to the closure (smallest enclosing).
	got := ids(idx.enclosingForRange(6, 7))
	require.Equal(t, []string{"f.go::Outer.closure"}, got)

	// A range spanning the closure boundary picks up both the closure and Outer.
	got = ids(idx.enclosingForRange(3, 7))
	require.ElementsMatch(t, []string{"f.go::Outer", "f.go::Outer.closure"}, got)

	// A range spanning two functions yields both.
	got = ids(idx.enclosingForRange(18, 24))
	require.ElementsMatch(t, []string{"f.go::Outer", "f.go::Second"}, got)

	// A range covering no symbol yields nothing.
	require.Empty(t, idx.enclosingForRange(40, 50))

	// Degenerate range collapses to the start line.
	got = ids(idx.enclosingForRange(25, 24))
	require.Equal(t, []string{"f.go::Second"}, got)
}

func ids(nodes []*graph.Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.ID)
	}
	return out
}

func TestSymbolsForRangesHandler(t *testing.T) {
	srv, g := setupNavServer(t)

	startNode := navFindMethod(t, g, "Start")
	start := g.GetNode(startNode)
	require.NotNil(t, start)

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"path":       "svc.go",
		"start_line": float64(start.StartLine),
		"end_line":   float64(start.EndLine),
	}
	res, err := srv.handleSymbolsForRanges(context.Background(), req)
	require.NoError(t, err)
	require.False(t, res.IsError, "handler errored: %s", toolResultText(res))

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &payload))

	syms, _ := payload["symbols"].([]any)
	require.NotEmpty(t, syms, "Start's range should resolve to at least one symbol")
	found := false
	for _, s := range syms {
		m := s.(map[string]any)
		if m["name"] == "Start" {
			found = true
		}
	}
	require.True(t, found, "expected the Start method among resolved symbols, got %v", payload["symbols"])
}
