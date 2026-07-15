package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/analysis"
	"github.com/zzet/gortex/internal/graph"
)

// newGapsTestServer builds a server pre-seeded with a small synthetic
// graph the tests can layer over. Nodes are deliberately wired so each
// rollup category has a planted, predictable instance.
func newGapsTestServer(t *testing.T) *Server {
	t.Helper()
	g := graph.New()

	// A — disconnected function (no edges).
	g.AddNode(&graph.Node{ID: "p/lonely.go::A", Name: "A", Kind: graph.KindFunction, FilePath: "p/lonely.go", StartLine: 10})

	// B — hub function, high fan-in; will be untested when we omit coverage.
	g.AddNode(&graph.Node{ID: "p/hub.go::B", Name: "B", Kind: graph.KindFunction, FilePath: "p/hub.go", StartLine: 5})
	// C, D, E — call B (giving B fan_in=3).
	for _, id := range []string{"p/caller1.go::C", "p/caller2.go::D", "p/caller3.go::E"} {
		g.AddNode(&graph.Node{ID: id, Name: id, Kind: graph.KindFunction, FilePath: id})
		g.AddEdge(&graph.Edge{From: id, To: "p/hub.go::B", Kind: graph.EdgeCalls})
	}

	// F — function with coverage above threshold; should NOT be flagged.
	g.AddNode(&graph.Node{
		ID:        "p/covered.go::F",
		Name:      "F",
		Kind:      graph.KindFunction,
		FilePath:  "p/covered.go",
		StartLine: 12,
		Meta:      map[string]any{"coverage_pct": 90.0},
	})
	// Give F one caller so it's not also "disconnected".
	g.AddNode(&graph.Node{ID: "p/caller4.go::G", Name: "G", Kind: graph.KindFunction, FilePath: "p/caller4.go"})
	g.AddEdge(&graph.Edge{From: "p/caller4.go::G", To: "p/covered.go::F", Kind: graph.EdgeCalls})

	s := &Server{
		graph:      g,
		session:    newSessionState(),
		tokenStats: &tokenStats{},
		symHistory: &symbolHistory{entries: make(map[string][]SymbolModification)},
		sessions:   newSessionMap(),
		toolScopes: newScopeRegistry(),
	}
	return s
}

func setCommunitiesForTest(s *Server, communities []analysis.Community) {
	installCommunitiesForTest(s, &analysis.CommunityResult{Communities: communities})
}

func callGapsHandler(t *testing.T, s *Server, args map[string]any) map[string]any {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := s.handleGetKnowledgeGaps(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.False(t, res.IsError, "handler error: %+v", res.Content)
	tc, ok := res.Content[0].(mcp.TextContent)
	require.True(t, ok)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &m))
	return m
}

func TestKnowledgeGaps_DisconnectedSurfaced(t *testing.T) {
	s := newGapsTestServer(t)
	out := callGapsHandler(t, s, map[string]any{})

	disc, _ := out["disconnected_nodes"].([]any)
	require.NotEmpty(t, disc)
	first := disc[0].(map[string]any)
	assert.Equal(t, "p/lonely.go::A", first["id"], "the only disconnected node should surface")
}

func TestKnowledgeGaps_UntestedHotspotFlagged(t *testing.T) {
	s := newGapsTestServer(t)
	out := callGapsHandler(t, s, map[string]any{})

	hotspots, _ := out["untested_hotspots"].([]any)
	require.NotEmpty(t, hotspots, "B has 3 callers and no coverage data — must be flagged")
	top := hotspots[0].(map[string]any)
	assert.Equal(t, "p/hub.go::B", top["id"])
	assert.EqualValues(t, 3, top["fan_in"].(float64))
	assert.Equal(t, false, top["has_coverage"])
}

func TestKnowledgeGaps_CoveredHotspotNotFlagged(t *testing.T) {
	s := newGapsTestServer(t)
	out := callGapsHandler(t, s, map[string]any{
		"min_coverage_pct": 50.0,
	})

	hotspots, _ := out["untested_hotspots"].([]any)
	for _, h := range hotspots {
		m := h.(map[string]any)
		assert.NotEqual(t, "p/covered.go::F", m["id"], "F's 90%% coverage clears the 50%% threshold")
	}
}

func TestKnowledgeGaps_ThinAndSingleFileCommunities(t *testing.T) {
	s := newGapsTestServer(t)
	setCommunitiesForTest(s, []analysis.Community{
		// Thin: 2 members.
		{ID: "c-tiny", Label: "tiny", Size: 2, Files: []string{"a.go", "b.go"}, Members: []string{"x", "y"}},
		// Single-file: 5 members all from one file.
		{ID: "c-mono", Label: "mono", Size: 5, Files: []string{"single.go"}, Members: []string{"m1", "m2", "m3", "m4", "m5"}},
		// Healthy: above threshold and multi-file — should NOT appear in either bucket.
		{ID: "c-fat", Label: "fat", Size: 10, Files: []string{"a.go", "b.go", "c.go"}, Members: []string{}},
	})

	out := callGapsHandler(t, s, map[string]any{})

	thin, _ := out["thin_communities"].([]any)
	require.Len(t, thin, 1)
	assert.Equal(t, "c-tiny", thin[0].(map[string]any)["id"])

	single, _ := out["single_file_communities"].([]any)
	require.Len(t, single, 1)
	assert.Equal(t, "c-mono", single[0].(map[string]any)["id"])
}

func TestKnowledgeGaps_PathPrefixFilter(t *testing.T) {
	s := newGapsTestServer(t)
	out := callGapsHandler(t, s, map[string]any{
		"path_prefix": "p/lonely",
	})

	// Only A matches path_prefix.
	disc, _ := out["disconnected_nodes"].([]any)
	require.Len(t, disc, 1)
	assert.Equal(t, "p/lonely.go::A", disc[0].(map[string]any)["id"])

	// Hotspot B is in p/hub.go — outside the prefix; must be empty.
	hotspots, _ := out["untested_hotspots"].([]any)
	assert.Empty(t, hotspots)
}

func TestKnowledgeGaps_ThresholdsEchoed(t *testing.T) {
	s := newGapsTestServer(t)
	out := callGapsHandler(t, s, map[string]any{
		"thin_community_size": 5,
		"min_coverage_pct":    75.0,
		"hotspot_limit":       5,
		"limit_per_category":  10,
	})

	th := out["thresholds"].(map[string]any)
	assert.EqualValues(t, 5, th["thin_community_size"].(float64))
	assert.EqualValues(t, 75, th["min_coverage_pct"].(float64))
	assert.EqualValues(t, 5, th["hotspot_limit"].(float64))
	assert.EqualValues(t, 10, th["limit_per_category"].(float64))
}

func TestKnowledgeGaps_LimitPerCategoryHonored(t *testing.T) {
	s := newGapsTestServer(t)
	// Add 5 disconnected nodes; cap at 3 should yield 3.
	for i := range 5 {
		id := "p/extra.go::Dx" + string(rune('A'+i))
		s.graph.AddNode(&graph.Node{ID: id, Name: id, Kind: graph.KindFunction, FilePath: "p/extra.go", StartLine: i + 1})
	}

	out := callGapsHandler(t, s, map[string]any{
		"limit_per_category": 3,
	})
	disc, _ := out["disconnected_nodes"].([]any)
	assert.LessOrEqual(t, len(disc), 3, "limit_per_category caps each rollup")
}

func TestKnowledgeGaps_SummaryCounts(t *testing.T) {
	s := newGapsTestServer(t)
	setCommunitiesForTest(s, []analysis.Community{
		{ID: "c1", Label: "x", Size: 2, Files: []string{"x.go"}},
	})

	out := callGapsHandler(t, s, map[string]any{})
	summary := out["summary"].(map[string]any)

	assert.EqualValues(t, 1, summary["disconnected_count"].(float64), "exactly one disconnected node")
	assert.EqualValues(t, 1, summary["thin_count"].(float64))
	assert.EqualValues(t, 1, summary["single_file_count"].(float64), "single-file community also matches")
	assert.GreaterOrEqual(t, summary["untested_count"].(float64), 1.0)
}
