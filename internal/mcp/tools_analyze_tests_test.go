package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

func newTestsAsEdgesServer(t *testing.T) *Server {
	t.Helper()
	g := graph.New()
	g.AddNode(&graph.Node{ID: "foo.go::Foo", Kind: graph.KindFunction, Name: "Foo", FilePath: "foo.go", StartLine: 10})
	g.AddNode(&graph.Node{ID: "bar.go::Bar", Kind: graph.KindFunction, Name: "Bar", FilePath: "bar.go", StartLine: 20})
	g.AddNode(&graph.Node{ID: "foo_test.go::TestA", Kind: graph.KindFunction, Name: "TestA", FilePath: "foo_test.go", StartLine: 1})
	g.AddNode(&graph.Node{ID: "foo_test.go::TestB", Kind: graph.KindFunction, Name: "TestB", FilePath: "foo_test.go", StartLine: 30})
	// EdgeTests: From = test, To = exercised symbol.
	g.AddEdge(&graph.Edge{From: "foo_test.go::TestA", To: "foo.go::Foo", Kind: graph.EdgeTests})
	g.AddEdge(&graph.Edge{From: "foo_test.go::TestB", To: "foo.go::Foo", Kind: graph.EdgeTests})
	g.AddEdge(&graph.Edge{From: "foo_test.go::TestA", To: "bar.go::Bar", Kind: graph.EdgeTests})

	return &Server{
		graph:      g,
		session:    newSessionState(),
		tokenStats: &tokenStats{},
		symHistory: &symbolHistory{entries: make(map[string][]SymbolModification)},
		sessions:   newSessionMap(),
		toolScopes: newScopeRegistry(),
	}
}

func callTestsAsEdges(t *testing.T, s *Server, args map[string]any) (map[string]any, bool) {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := s.handleAnalyzeTestsAsEdges(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, res)
	tc, ok := res.Content[0].(mcp.TextContent)
	require.True(t, ok)
	if res.IsError {
		return nil, true
	}
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &m))
	return m, false
}

func TestTestsAsEdges_GroupBySymbol(t *testing.T) {
	s := newTestsAsEdgesServer(t)
	out, isErr := callTestsAsEdges(t, s, map[string]any{})
	require.False(t, isErr)
	require.Equal(t, "symbol", out["group_by"])

	rows, _ := out["rows"].([]any)
	require.Len(t, rows, 2)
	// Most-covered first: Foo (2 tests) before Bar (1 test).
	first, _ := rows[0].(map[string]any)
	require.Equal(t, "foo.go::Foo", first["id"])
	require.Equal(t, float64(2), first["edge_count"])
	tests, _ := first["tests"].([]any)
	require.Len(t, tests, 2)

	summary, _ := out["summary"].(map[string]any)
	require.Equal(t, float64(3), summary["test_edges"])
	require.Equal(t, float64(2), summary["tested_symbols"])
	require.Equal(t, float64(2), summary["test_functions"])
}

func TestTestsAsEdges_GroupByTest(t *testing.T) {
	s := newTestsAsEdgesServer(t)
	out, isErr := callTestsAsEdges(t, s, map[string]any{"group_by": "test"})
	require.False(t, isErr)
	require.Equal(t, "test", out["group_by"])

	rows, _ := out["rows"].([]any)
	require.Len(t, rows, 2)
	// TestA exercises 2 symbols, TestB exercises 1.
	first, _ := rows[0].(map[string]any)
	require.Equal(t, "foo_test.go::TestA", first["id"])
	covers, _ := first["covers"].([]any)
	require.Len(t, covers, 2)
}

func TestTestsAsEdges_PathPrefixFilter(t *testing.T) {
	s := newTestsAsEdgesServer(t)
	out, _ := callTestsAsEdges(t, s, map[string]any{"path_prefix": "bar"})
	rows, _ := out["rows"].([]any)
	require.Len(t, rows, 1)
	first, _ := rows[0].(map[string]any)
	require.Equal(t, "bar.go::Bar", first["id"])
}

func TestTestsAsEdges_InvalidGroupBy(t *testing.T) {
	s := newTestsAsEdgesServer(t)
	_, isErr := callTestsAsEdges(t, s, map[string]any{"group_by": "bogus"})
	require.True(t, isErr)
}

func TestTestsAsEdges_DispatchedViaAnalyze(t *testing.T) {
	s := newTestsAsEdgesServer(t)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"kind": "tests_as_edges"}
	res, err := s.handleAnalyze(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.False(t, res.IsError, "dispatcher must route kind=tests_as_edges without error")
}
