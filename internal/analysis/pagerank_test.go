package analysis

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestComputePageRank_EmptyGraph(t *testing.T) {
	r := ComputePageRank(graph.New())
	if len(r.Scores) != 0 {
		t.Errorf("empty graph should yield no scores, got %d", len(r.Scores))
	}
	if r.Max != 0 {
		t.Errorf("Max = %f, want 0", r.Max)
	}
}

func TestComputePageRank_HubScoresHighest(t *testing.T) {
	g := graph.New()
	for _, id := range []string{"hub", "a", "b", "c", "d"} {
		g.AddNode(&graph.Node{ID: id, Kind: graph.KindFunction, Name: id})
	}
	// Everyone calls hub; hub calls nobody. Rank flows into hub.
	for _, caller := range []string{"a", "b", "c", "d"} {
		g.AddEdge(&graph.Edge{From: caller, To: "hub", Kind: graph.EdgeCalls})
	}

	r := ComputePageRank(g)
	if r.ScoreOf("hub") <= r.ScoreOf("a") {
		t.Errorf("hub (%f) should outrank a leaf caller a (%f)", r.ScoreOf("hub"), r.ScoreOf("a"))
	}
	if r.Max != r.ScoreOf("hub") {
		t.Errorf("Max should equal the hub score: max=%f hub=%f", r.Max, r.ScoreOf("hub"))
	}
	// PageRank is a probability distribution — scores sum to ~1.
	var sum float64
	for _, v := range r.Scores {
		sum += v
	}
	if sum < 0.95 || sum > 1.05 {
		t.Errorf("scores should sum to ~1, got %f", sum)
	}
}

func TestComputePageRank_TransitiveImportance(t *testing.T) {
	g := graph.New()
	for _, id := range []string{"top", "mid", "leaf"} {
		g.AddNode(&graph.Node{ID: id, Kind: graph.KindFunction, Name: id})
	}
	// top -> mid -> leaf. leaf is depended on most transitively.
	g.AddEdge(&graph.Edge{From: "top", To: "mid", Kind: graph.EdgeCalls})
	g.AddEdge(&graph.Edge{From: "mid", To: "leaf", Kind: graph.EdgeCalls})

	r := ComputePageRank(g)
	if r.ScoreOf("leaf") <= r.ScoreOf("top") {
		t.Errorf("leaf (%f) should outrank top (%f)", r.ScoreOf("leaf"), r.ScoreOf("top"))
	}
}
