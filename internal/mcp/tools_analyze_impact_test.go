package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/analysis"
	"github.com/zzet/gortex/internal/graph"
)

// newImpactTestServer builds a server whose graph has one clear
// blast-radius hub (`hub`) plus a set of leaf callers.
func newImpactTestServer(t *testing.T) *Server {
	t.Helper()
	g := graph.New()
	g.AddNode(&graph.Node{
		ID: "core.go::Hub", Kind: graph.KindFunction, Name: "Hub",
		FilePath: "core.go", StartLine: 10,
		Meta: map[string]any{"complexity": 12},
	})
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		g.AddNode(&graph.Node{
			ID: "callers.go::" + id, Kind: graph.KindFunction, Name: id,
			FilePath: "callers.go", StartLine: 1,
		})
		g.AddEdge(&graph.Edge{From: "callers.go::" + id, To: "core.go::Hub", Kind: graph.EdgeCalls})
	}

	s := &Server{
		graph:      g,
		session:    newSessionState(),
		tokenStats: &tokenStats{},
		symHistory: &symbolHistory{entries: make(map[string][]SymbolModification)},
		sessions:   newSessionMap(),
		toolScopes: newScopeRegistry(),
	}
	s.analysisMu.Lock()
	s.pageRank = analysis.ComputePageRank(g)
	s.analysisMu.Unlock()
	installCommunitiesForTest(s, analysis.DetectCommunities(g))
	// Consume the co-change once-guard so ensureCoChange does not
	// shell out to git during the test.
	s.cochangeOnce.Do(func() {})
	return s
}

func callAnalyzeImpact(t *testing.T, s *Server, args map[string]any) map[string]any {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := s.handleAnalyzeImpactComposite(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.False(t, res.IsError)
	tc, ok := res.Content[0].(mcp.TextContent)
	require.True(t, ok)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &m))
	return m
}

func TestAnalyzeImpact_HubRanksAboveLeaves(t *testing.T) {
	s := newImpactTestServer(t)
	out := callAnalyzeImpact(t, s, map[string]any{})

	symbols, _ := out["symbols"].([]any)
	require.NotEmpty(t, symbols)
	// The hub — five callers, complexity 12 — outranks every leaf.
	first, _ := symbols[0].(map[string]any)
	require.Equal(t, "core.go::Hub", first["id"])

	score, _ := first["score"].(float64)
	require.Greater(t, score, 0.0)
	// Per-axis breakdown is present and explainable.
	require.Contains(t, first, "centrality")
	require.Contains(t, first, "reach")
	require.Contains(t, first, "complexity")
	require.Contains(t, first, "co_change")
	require.Contains(t, first, "community")
	require.Equal(t, float64(5), first["fan_in"])
	require.Equal(t, float64(12), first["cyclomatic"])
}

func TestAnalyzeImpact_IDsFilter(t *testing.T) {
	s := newImpactTestServer(t)
	out := callAnalyzeImpact(t, s, map[string]any{"ids": "core.go::Hub"})
	symbols, _ := out["symbols"].([]any)
	require.Len(t, symbols, 1)
	first, _ := symbols[0].(map[string]any)
	require.Equal(t, "core.go::Hub", first["id"])
}

func TestAnalyzeImpact_WeightsInResponse(t *testing.T) {
	s := newImpactTestServer(t)
	out := callAnalyzeImpact(t, s, map[string]any{})
	weights, ok := out["weights"].(map[string]any)
	require.True(t, ok)
	for _, axis := range []string{"centrality", "reach", "complexity", "co_change", "community"} {
		require.Contains(t, weights, axis)
	}
}

func TestAnalyzeImpact_MinScoreFilter(t *testing.T) {
	s := newImpactTestServer(t)
	// A threshold above every leaf's score keeps only the hub.
	out := callAnalyzeImpact(t, s, map[string]any{"min_score": 99.0})
	symbols, _ := out["symbols"].([]any)
	require.Empty(t, symbols)
}

func TestAnalyzeImpact_DispatchedViaAnalyze(t *testing.T) {
	s := newImpactTestServer(t)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"kind": "impact"}
	res, err := s.handleAnalyze(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.False(t, res.IsError)
}
