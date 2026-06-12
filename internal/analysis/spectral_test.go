package analysis

import (
	"fmt"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// clique adds n function nodes with the given id prefix and a call
// edge between every pair — a maximally cohesive cluster.
func clique(g *graph.Graph, prefix string, n int) []string {
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%s%d", prefix, i)
		ids[i] = id
		g.AddNode(&graph.Node{ID: id, Kind: graph.KindFunction, Name: id, FilePath: prefix + ".go"})
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			g.AddEdge(&graph.Edge{From: ids[i], To: ids[j], Kind: graph.EdgeCalls})
		}
	}
	return ids
}

func TestSpectralClusters_SeparatesTwoCliques(t *testing.T) {
	g := graph.New()
	a := clique(g, "a", 12)
	b := clique(g, "b", 12)
	// A single bridge edge — the Fiedler cut should run through it.
	g.AddEdge(&graph.Edge{From: a[0], To: b[0], Kind: graph.EdgeCalls})

	res := SpectralClusters(g)
	if len(res.Communities) != 2 {
		t.Fatalf("expected 2 communities, got %d: %+v", len(res.Communities), res.Communities)
	}

	// No community may mix an a-clique node with a b-clique node.
	for _, c := range res.Communities {
		var sawA, sawB bool
		for _, m := range c.Members {
			if m[0] == 'a' {
				sawA = true
			} else {
				sawB = true
			}
		}
		if sawA && sawB {
			t.Errorf("community %s mixes the two cliques: %v", c.ID, c.Members)
		}
	}

	// Every node is assigned.
	if len(res.NodeToComm) != 24 {
		t.Errorf("NodeToComm covers %d nodes, want 24", len(res.NodeToComm))
	}
	// A well-separated partition has clearly positive modularity.
	if res.Modularity <= 0 {
		t.Errorf("modularity = %f, want > 0 for two near-disjoint cliques", res.Modularity)
	}
}

func TestSpectralClusters_EmptyGraph(t *testing.T) {
	res := SpectralClusters(graph.New())
	if len(res.Communities) != 0 {
		t.Errorf("empty graph should yield no communities, got %d", len(res.Communities))
	}
	if res.NodeToComm == nil {
		t.Error("NodeToComm must be non-nil even for an empty graph")
	}
}

func TestSpectralClusters_SmallComponentNotSplit(t *testing.T) {
	g := graph.New()
	// A single 6-node clique — well below the bisection floor, so it
	// must come back as exactly one community.
	clique(g, "c", 6)
	res := SpectralClusters(g)
	if len(res.Communities) != 1 {
		t.Fatalf("a small clique should be one community, got %d", len(res.Communities))
	}
	if res.Communities[0].Size != 6 {
		t.Errorf("community size = %d, want 6", res.Communities[0].Size)
	}
}

func TestSpectralClusters_DisconnectedComponents(t *testing.T) {
	g := graph.New()
	clique(g, "x", 5)
	clique(g, "y", 5)
	clique(g, "z", 5)
	// No bridges — three disconnected cliques.
	res := SpectralClusters(g)
	if len(res.Communities) != 3 {
		t.Fatalf("three disconnected cliques should be three communities, got %d", len(res.Communities))
	}
}
