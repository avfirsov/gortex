package analysis

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestBuildAdjacencySnapshot_Empty(t *testing.T) {
	snap := BuildAdjacencySnapshot(graph.New())
	if snap.NodeCount() != 0 {
		t.Errorf("empty graph should have 0 nodes, got %d", snap.NodeCount())
	}
	if snap.EdgeCount() != 0 {
		t.Errorf("empty graph should have 0 edges, got %d", snap.EdgeCount())
	}
	// A nil-safe PersonalizedPageRank on an empty snapshot returns an
	// empty map, not a panic.
	if got := snap.PersonalizedPageRank([]string{"x"}, 0); len(got) != 0 {
		t.Errorf("expected empty result on empty snapshot, got %v", got)
	}
}

func TestBuildAdjacencySnapshot_OnlyCallAndReferenceEdges(t *testing.T) {
	g := graph.New()
	for _, id := range []string{"a", "b", "c", "d"} {
		g.AddNode(&graph.Node{ID: id, Kind: graph.KindFunction, Name: id})
	}
	// a -> b (calls), a -> c (references), a -> d (imports — excluded).
	g.AddEdge(&graph.Edge{From: "a", To: "b", Kind: graph.EdgeCalls})
	g.AddEdge(&graph.Edge{From: "a", To: "c", Kind: graph.EdgeReferences})
	g.AddEdge(&graph.Edge{From: "a", To: "d", Kind: graph.EdgeImports})

	snap := BuildAdjacencySnapshot(g)
	if snap.NodeCount() != 4 {
		t.Fatalf("expected 4 nodes, got %d", snap.NodeCount())
	}
	// Only the calls + references edges are captured.
	if snap.EdgeCount() != 2 {
		t.Errorf("expected 2 captured edges (calls+references), got %d", snap.EdgeCount())
	}
}

func TestBuildAdjacencySnapshot_SkipsDanglingEndpoints(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{ID: "a", Kind: graph.KindFunction, Name: "a"})
	// Edge to a node that does not exist in the graph — must be skipped
	// so the dense index stays consistent.
	g.AddEdge(&graph.Edge{From: "a", To: "ghost", Kind: graph.EdgeCalls})

	snap := BuildAdjacencySnapshot(g)
	if snap.NodeCount() != 1 {
		t.Fatalf("expected 1 node, got %d", snap.NodeCount())
	}
	if snap.EdgeCount() != 0 {
		t.Errorf("dangling edge must be skipped, got %d edges", snap.EdgeCount())
	}
}

func TestPersonalizedPageRank_ConcentratesNearSeed(t *testing.T) {
	g := graph.New()
	// Two disjoint chains. Seed one of them; the seeded chain must
	// score far above the unrelated chain.
	for _, id := range []string{"s", "s1", "s2", "u", "u1", "u2"} {
		g.AddNode(&graph.Node{ID: id, Kind: graph.KindFunction, Name: id})
	}
	g.AddEdge(&graph.Edge{From: "s", To: "s1", Kind: graph.EdgeCalls})
	g.AddEdge(&graph.Edge{From: "s1", To: "s2", Kind: graph.EdgeCalls})
	g.AddEdge(&graph.Edge{From: "u", To: "u1", Kind: graph.EdgeCalls})
	g.AddEdge(&graph.Edge{From: "u1", To: "u2", Kind: graph.EdgeCalls})

	snap := BuildAdjacencySnapshot(g)
	scores := snap.PersonalizedPageRank([]string{"s"}, 0)

	// The seed and its reachable neighbours carry mass; the unrelated
	// chain is never reached from the seed, so it stays at zero.
	if scores["s"] <= 0 {
		t.Errorf("seed should carry positive mass, got %f", scores["s"])
	}
	if scores["s1"] <= 0 {
		t.Errorf("seed-reachable node should carry mass, got %f", scores["s1"])
	}
	if scores["u"] != 0 || scores["u1"] != 0 || scores["u2"] != 0 {
		t.Errorf("the unrelated chain should be unreachable from the seed: u=%f u1=%f u2=%f",
			scores["u"], scores["u1"], scores["u2"])
	}
	// The seed itself, kept warm by restart, outranks its downstream.
	if scores["s"] <= scores["s2"] {
		t.Errorf("restart should keep the seed (%f) above its downstream (%f)", scores["s"], scores["s2"])
	}
}

func TestPersonalizedPageRank_MultiSeedSharesRestartMass(t *testing.T) {
	g := graph.New()
	for _, id := range []string{"p", "q", "x"} {
		g.AddNode(&graph.Node{ID: id, Kind: graph.KindFunction, Name: id})
	}
	// Both seeds call a shared node x.
	g.AddEdge(&graph.Edge{From: "p", To: "x", Kind: graph.EdgeCalls})
	g.AddEdge(&graph.Edge{From: "q", To: "x", Kind: graph.EdgeCalls})

	snap := BuildAdjacencySnapshot(g)
	scores := snap.PersonalizedPageRank([]string{"p", "q"}, 0)

	if scores["p"] <= 0 || scores["q"] <= 0 {
		t.Errorf("both seeds should carry mass: p=%f q=%f", scores["p"], scores["q"])
	}
	// x is fed by both seeds.
	if scores["x"] <= 0 {
		t.Errorf("the shared downstream node should accumulate mass, got %f", scores["x"])
	}
}

func TestPersonalizedPageRank_NoSeedInSnapshot(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{ID: "a", Kind: graph.KindFunction, Name: "a"})
	snap := BuildAdjacencySnapshot(g)

	// A seed absent from the snapshot yields an empty result (no walk
	// to run), never a panic.
	if got := snap.PersonalizedPageRank([]string{"absent"}, 0); len(got) != 0 {
		t.Errorf("expected empty result for absent seed, got %v", got)
	}
}

func TestPersonalizedPageRank_ProvenanceWeightingMatchesPageRankEdgeSet(t *testing.T) {
	g := graph.New()
	for _, id := range []string{"a", "b"} {
		g.AddNode(&graph.Node{ID: id, Kind: graph.KindFunction, Name: id})
	}
	// A high-confidence AST-resolved call: it must be captured with a
	// non-zero weight so the seeded walk can flow along it.
	g.AddEdge(&graph.Edge{From: "a", To: "b", Kind: graph.EdgeCalls, Origin: graph.OriginASTResolved})

	snap := BuildAdjacencySnapshot(g)
	scores := snap.PersonalizedPageRank([]string{"a"}, 0)
	if scores["b"] <= 0 {
		t.Errorf("mass should flow from seed a to b along the weighted edge, got b=%f", scores["b"])
	}
}
