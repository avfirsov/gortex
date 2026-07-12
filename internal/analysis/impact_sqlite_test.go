package analysis

import (
	"path/filepath"
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_sqlite"
)

func TestAnalyzeImpactSQLiteRepeatedIsStable(t *testing.T) {
	s, err := store_sqlite.Open(filepath.Join(t.TempDir(), "impact.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	nodes := []*graph.Node{
		{ID: "seed", Kind: graph.KindFunction, Name: "seed", FilePath: "seed.go"},
		{ID: "direct", Kind: graph.KindFunction, Name: "direct", FilePath: "direct.go"},
		{ID: "transitive", Kind: graph.KindFunction, Name: "transitive", FilePath: "transitive.go"},
	}
	edges := []*graph.Edge{
		{From: "direct", To: "seed", Kind: graph.EdgeCalls, Confidence: 0.9},
		{From: "transitive", To: "direct", Kind: graph.EdgeCalls, Confidence: 0.8},
	}
	s.AddBatch(nodes, edges)

	first := AnalyzeImpact(s, []string{"seed"}, nil, nil)
	for i := 0; i < 3; i++ {
		got := AnalyzeImpact(s, []string{"seed"}, nil, nil)
		if got.TotalAffected != first.TotalAffected || got.Risk != first.Risk || got.Summary != first.Summary {
			t.Fatalf("impact call %d changed: first=%+v got=%+v", i+2, first, got)
		}
		if got.TotalAffected != 2 {
			t.Fatalf("impact call %d total = %d, want 2", i+2, got.TotalAffected)
		}
	}
	if got := s.EdgeCount(); got != len(edges) {
		t.Fatalf("edge count after repeated impact = %d, want %d", got, len(edges))
	}
}

func TestAnalyzeImpactTruncationIsConservative(t *testing.T) {
	g := graph.New()
	const callers = 5100
	nodes := make([]*graph.Node, 0, callers+1)
	edges := make([]*graph.Edge, 0, callers)
	nodes = append(nodes, &graph.Node{ID: "seed", Kind: graph.KindFunction, Name: "seed"})
	for i := 0; i < callers; i++ {
		id := "caller-" + string(rune(i+1))
		nodes = append(nodes, &graph.Node{ID: id, Kind: graph.KindFunction, Name: id})
		edges = append(edges, &graph.Edge{From: id, To: "seed", Kind: graph.EdgeCalls})
	}
	g.AddBatch(nodes, edges)

	result := AnalyzeImpact(g, []string{"seed"}, nil, nil)
	if !result.Truncated || !result.LowerBound {
		t.Fatalf("truncated/lower_bound = %v/%v, want true/true", result.Truncated, result.LowerBound)
	}
	if result.Risk == RiskLow {
		t.Fatalf("truncated impact returned false-safe LOW risk: %+v", result)
	}
	if result.TotalAffected == 0 {
		t.Fatal("bounded impact discarded all observed callers")
	}
}
