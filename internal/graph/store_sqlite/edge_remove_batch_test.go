package store_sqlite_test

import (
	"path/filepath"
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_sqlite"
)

func TestRemoveEdgesExactPreservesSameEndpointSibling(t *testing.T) {
	store, err := store_sqlite.Open(filepath.Join(t.TempDir(), "remove-exact.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	store.AddBatch([]*graph.Node{
		{ID: "caller", Kind: graph.KindFunction, Name: "caller"},
		{ID: "target", Kind: graph.KindFunction, Name: "target"},
	}, []*graph.Edge{
		{From: "caller", To: "target", Kind: graph.EdgeCalls, FilePath: "a.go", Line: 10},
		{From: "caller", To: "target", Kind: graph.EdgeCalls, FilePath: "a.go", Line: 20},
	})

	edges := store.GetOutEdges("caller")
	var remove *graph.Edge
	for _, edge := range edges {
		if edge.Line == 10 {
			remove = edge
		}
	}
	if remove == nil {
		t.Fatal("line-10 edge missing")
	}
	if got := graph.RemoveEdgesExact(store, []*graph.Edge{remove, remove}); got != 1 {
		t.Fatalf("removed = %d, want 1 exact deduplicated identity", got)
	}
	remaining := store.GetOutEdges("caller")
	if len(remaining) != 1 || remaining[0].Line != 20 {
		t.Fatalf("remaining edges = %#v, want only line 20 sibling", remaining)
	}
}
