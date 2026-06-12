package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

// seedProximityGraph injects a hub-and-spoke call graph: the seed h
// calls a and b; a and b both call shared. shared is reachable from the
// seed along two short paths, so a seeded random walk should rank it
// well — the signal distance ranking (single shortest path) cannot see.
func seedProximityGraph(t *testing.T, srv *Server) {
	t.Helper()
	g := srv.graph
	add := func(id, name, file string) {
		g.AddNode(&graph.Node{ID: id, Kind: graph.KindFunction, Name: name, FilePath: file, Language: "go"})
	}
	add("h.go::H", "H", "h.go")
	add("a.go::A", "A", "a.go")
	add("b.go::B", "B", "b.go")
	add("s.go::S", "S", "s.go")
	add("far.go::Far", "Far", "far.go")

	g.AddEdge(&graph.Edge{From: "h.go::H", To: "a.go::A", Kind: graph.EdgeCalls, FilePath: "h.go", Line: 1, Origin: graph.OriginASTResolved})
	g.AddEdge(&graph.Edge{From: "h.go::H", To: "b.go::B", Kind: graph.EdgeCalls, FilePath: "h.go", Line: 2, Origin: graph.OriginASTResolved})
	g.AddEdge(&graph.Edge{From: "a.go::A", To: "s.go::S", Kind: graph.EdgeCalls, FilePath: "a.go", Line: 1, Origin: graph.OriginASTResolved})
	g.AddEdge(&graph.Edge{From: "b.go::B", To: "s.go::S", Kind: graph.EdgeCalls, FilePath: "b.go", Line: 1, Origin: graph.OriginASTResolved})
	g.AddEdge(&graph.Edge{From: "s.go::S", To: "far.go::Far", Kind: graph.EdgeCalls, FilePath: "s.go", Line: 1, Origin: graph.OriginASTResolved})
}

func TestContextClosure_ProximityRankPopulatesScores(t *testing.T) {
	srv, _ := setupTestServer(t)
	seedProximityGraph(t, srv)
	// Rebuild the adjacency snapshot over the augmented graph.
	srv.RunAnalysis()

	out := extractTextResult(t, callTool(t, srv, "context_closure", map[string]any{
		"symbols":    "h.go::H",
		"edge_kinds": "calls",
		"rank":       "proximity",
	}))

	assert.Equal(t, "proximity", out["rank"])

	members, ok := out["members"].([]any)
	require.True(t, ok)
	// Every member carries a proximity score under proximity ranking,
	// and the seed's score is positive.
	var sawSeedScore bool
	for _, m := range members {
		row := m.(map[string]any)
		_, hasProx := row["proximity"]
		assert.True(t, hasProx, "proximity ranking must attach a proximity score to %v", row["id"])
		if row["id"] == "h.go::H" {
			sawSeedScore = true
			assert.Greater(t, row["proximity"].(float64), 0.0)
		}
	}
	assert.True(t, sawSeedScore, "seed must appear in the member list")
}

func TestContextClosure_ProximityFallsBackBeforeAnalysis(t *testing.T) {
	srv, _ := setupTestServer(t)
	seedProximityGraph(t, srv)
	// Intentionally do NOT call RunAnalysis after seeding, then null the
	// snapshot so getAdjacency() returns nil — proximity must degrade to
	// distance ranking without error.
	srv.analysisMu.Lock()
	srv.adjacency = nil
	srv.analysisMu.Unlock()

	out := extractTextResult(t, callTool(t, srv, "context_closure", map[string]any{
		"symbols":    "h.go::H",
		"edge_kinds": "calls",
		"rank":       "proximity",
	}))
	// The tool still answers; with no snapshot the members carry no
	// proximity score (nil map) but the closure is intact.
	members, ok := out["members"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, members)
}

func TestServer_AdjacencySnapshotInvalidatesOnTokenChange(t *testing.T) {
	srv, _ := setupTestServer(t)
	seedProximityGraph(t, srv)
	srv.RunAnalysis()

	snap1 := srv.getAdjacency()
	require.NotNil(t, snap1)
	tok1 := srv.adjacencyToken
	n1 := snap1.NodeCount()

	// Mutate the graph (add a node + edge), then re-run analysis. The
	// snapshot must be rebuilt and the token must advance with the
	// graph identity.
	g := srv.graph
	g.AddNode(&graph.Node{ID: "new.go::New", Kind: graph.KindFunction, Name: "New", FilePath: "new.go", Language: "go"})
	g.AddEdge(&graph.Edge{From: "h.go::H", To: "new.go::New", Kind: graph.EdgeCalls, FilePath: "h.go", Line: 9, Origin: graph.OriginASTResolved})
	srv.RunAnalysis()

	snap2 := srv.getAdjacency()
	require.NotNil(t, snap2)
	tok2 := srv.adjacencyToken

	assert.NotEqual(t, tok1, tok2, "adjacency token must advance when the graph changes")
	assert.Greater(t, snap2.NodeCount(), n1, "rebuilt snapshot must reflect the added node")
}
