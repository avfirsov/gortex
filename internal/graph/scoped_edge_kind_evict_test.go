package graph

import (
	"context"
	"testing"
)

func TestGraphEvictEdgesFromSourcesByKindsMatchesScopedContract(t *testing.T) {
	g := New()
	g.AddBatch([]*Node{
		{ID: "a", Kind: KindMethod}, {ID: "b", Kind: KindMethod}, {ID: "target", Kind: KindField},
	}, []*Edge{
		{From: "a", To: "target", Kind: EdgeAccessesField, FilePath: "a.go", Line: 1},
		{From: "a", To: "target", Kind: EdgeAccessesField, FilePath: "a.go", Line: 2},
		{From: "a", To: "target", Kind: EdgeWrites, FilePath: "a.go", Line: 3},
		{From: "b", To: "target", Kind: EdgeAccessesField, FilePath: "b.go", Line: 4},
	})
	removed, err := g.EvictEdgesFromSourcesByKinds(context.Background(),
		[]string{"a", "a"}, []EdgeKind{EdgeAccessesField, EdgeAccessesField})
	if err != nil || removed != 2 {
		t.Fatalf("eviction = (%d, %v), want (2, nil)", removed, err)
	}
	if got := g.GetOutEdges("a"); len(got) != 1 || got[0].Kind != EdgeWrites {
		t.Fatalf("source a remainder = %#v, want writes only", got)
	}
	if got := g.GetOutEdges("b"); len(got) != 1 || got[0].Kind != EdgeAccessesField {
		t.Fatalf("source b was touched: %#v", got)
	}
}
