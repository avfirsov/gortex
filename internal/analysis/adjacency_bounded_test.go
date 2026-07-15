package analysis

import (
	"math"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestBuildBoundedAdjacencySnapshotParityOnCoveredFixture(t *testing.T) {
	g := graph.New()
	ids := []string{"a", "b", "c", "d", "e"}
	for _, id := range ids {
		g.AddNode(&graph.Node{ID: id, Name: id, Kind: graph.KindFunction})
	}
	for _, edge := range [][2]string{{"a", "b"}, {"b", "c"}, {"a", "d"}, {"d", "e"}} {
		g.AddEdge(&graph.Edge{From: edge[0], To: edge[1], Kind: graph.EdgeCalls})
	}

	full := BuildAdjacencySnapshot(g)
	bounded, stats := BuildBoundedAdjacencySnapshot(g, ids, 2, 100, 100)
	if stats.Truncated {
		t.Fatalf("covered fixture unexpectedly truncated: %+v", stats)
	}
	want := full.PersonalizedPageRankTopK([]string{"a"}, 0, 100)
	got := bounded.PersonalizedPageRankTopK([]string{"a"}, 0, 100)
	if len(got) != len(want) {
		t.Fatalf("score count mismatch: got=%d want=%d", len(got), len(want))
	}
	for id, wantScore := range want {
		if delta := math.Abs(got[id] - wantScore); delta > 1e-12 {
			t.Fatalf("score[%s] delta=%g got=%g want=%g", id, delta, got[id], wantScore)
		}
	}
}

func TestBuildBoundedAdjacencySnapshotReportsHardCaps(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{ID: "root", Name: "root", Kind: graph.KindFunction})
	for _, id := range []string{"a", "b", "c", "d"} {
		g.AddNode(&graph.Node{ID: id, Name: id, Kind: graph.KindFunction})
		g.AddEdge(&graph.Edge{From: "root", To: id, Kind: graph.EdgeCalls})
	}
	_, stats := BuildBoundedAdjacencySnapshot(g, []string{"root"}, 2, 3, 2)
	if !stats.Truncated {
		t.Fatalf("hard cap was not reported: %+v", stats)
	}
	if stats.NodeCount > 3 || stats.EdgeCount > 2 {
		t.Fatalf("hard cap exceeded: %+v", stats)
	}
}

type countingAdjacencyReader struct {
	graph.Reader
	nodeBatches int
	edgeBatches int
}

func (r *countingAdjacencyReader) GetNodesByIDs(ids []string) map[string]*graph.Node {
	r.nodeBatches++
	return r.Reader.GetNodesByIDs(ids)
}

func (r *countingAdjacencyReader) GetOutEdgesByNodeIDs(ids []string) map[string][]*graph.Edge {
	r.edgeBatches++
	return r.Reader.GetOutEdgesByNodeIDs(ids)
}

func TestBuildBoundedAdjacencySnapshotUsesBatchRounds(t *testing.T) {
	g := graph.New()
	roots := make([]string, 0, 64)
	for i := 0; i < 64; i++ {
		id := string(rune('a' + i))
		roots = append(roots, id)
		g.AddNode(&graph.Node{ID: id, Name: id, Kind: graph.KindFunction})
	}
	for i := 0; i+1 < len(roots); i++ {
		g.AddEdge(&graph.Edge{From: roots[i], To: roots[i+1], Kind: graph.EdgeReferences})
	}
	reader := &countingAdjacencyReader{Reader: g}
	_, stats := BuildBoundedAdjacencySnapshot(reader, roots, 2, 4096, 16384)
	if reader.edgeBatches != stats.EdgeBatches || reader.nodeBatches != stats.NodeBatches {
		t.Fatalf("receipt mismatch: reader nodes=%d edges=%d stats=%+v", reader.nodeBatches, reader.edgeBatches, stats)
	}
	if reader.edgeBatches != 1 || reader.nodeBatches != 2 {
		t.Fatalf("expected constant batch rounds, got nodes=%d edges=%d", reader.nodeBatches, reader.edgeBatches)
	}
}
