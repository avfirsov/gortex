package resolver

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestDetectCrossRepoEdgesForFilesUsesExactIncidentFrontier(t *testing.T) {
	g := graph.New()
	g.AddBatch([]*graph.Node{
		{ID: "a/a.go", Kind: graph.KindFile, Name: "a.go", FilePath: "a/a.go", RepoPrefix: "a"},
		{ID: "a/a.go::Call", Kind: graph.KindFunction, Name: "Call", FilePath: "a/a.go", RepoPrefix: "a"},
		{ID: "b/b.go", Kind: graph.KindFile, Name: "b.go", FilePath: "b/b.go", RepoPrefix: "b"},
		{ID: "b/b.go::Serve", Kind: graph.KindFunction, Name: "Serve", FilePath: "b/b.go", RepoPrefix: "b"},
		{ID: "c/c.go", Kind: graph.KindFile, Name: "c.go", FilePath: "c/c.go", RepoPrefix: "c"},
		{ID: "c/c.go::Call", Kind: graph.KindFunction, Name: "Call", FilePath: "c/c.go", RepoPrefix: "c"},
		{ID: "d/d.go", Kind: graph.KindFile, Name: "d.go", FilePath: "d/d.go", RepoPrefix: "d"},
		{ID: "d/d.go::Serve", Kind: graph.KindFunction, Name: "Serve", FilePath: "d/d.go", RepoPrefix: "d"},
	}, []*graph.Edge{
		{From: "a/a.go::Call", To: "b/b.go::Serve", Kind: graph.EdgeCalls, FilePath: "a/a.go", Line: 3},
		{From: "c/c.go::Call", To: "d/d.go::Serve", Kind: graph.EdgeCalls, FilePath: "c/c.go", Line: 4},
	})

	if got := DetectCrossRepoEdgesForFiles(g, []string{"b/b.go"}); got != 1 {
		t.Fatalf("emitted = %d, want incident edge only", got)
	}
	crossKind, ok := graph.CrossRepoKindFor(graph.EdgeCalls)
	if !ok {
		t.Fatal("calls has no cross-repo kind")
	}
	if !hasEdgeKindTo(g.GetOutEdges("a/a.go::Call"), crossKind, "b/b.go::Serve") {
		t.Fatal("incoming edge to changed target was not materialized")
	}
	if hasEdgeKindTo(g.GetOutEdges("c/c.go::Call"), crossKind, "d/d.go::Serve") {
		t.Fatal("unrelated cross-repo edge leaked outside exact frontier")
	}
}

func hasEdgeKindTo(edges []*graph.Edge, kind graph.EdgeKind, target string) bool {
	for _, edge := range edges {
		if edge != nil && edge.Kind == kind && edge.To == target {
			return true
		}
	}
	return false
}
