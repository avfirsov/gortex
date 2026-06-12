package lsp

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/semantic"
)

// TestAddOverrideEdges_LSPDispatch verifies the LSP-side override
// helper emits EdgeOverrides at the lsp_dispatch tier when the parent
// edge came from a typeHierarchy hop.
func TestAddOverrideEdges_LSPDispatch(t *testing.T) {
	g := graph.New()
	parent := &graph.Node{ID: "p.kt::Base", Kind: graph.KindType, Name: "Base", Language: "kotlin"}
	child := &graph.Node{ID: "p.kt::Child", Kind: graph.KindType, Name: "Child", Language: "kotlin"}
	pm := &graph.Node{ID: "p.kt::Base.run", Kind: graph.KindMethod, Name: "run", Language: "kotlin", FilePath: "p.kt", StartLine: 2}
	cm := &graph.Node{ID: "p.kt::Child.run", Kind: graph.KindMethod, Name: "run", Language: "kotlin", FilePath: "p.kt", StartLine: 5}
	for _, n := range []*graph.Node{parent, child, pm, cm} {
		g.AddNode(n)
	}
	g.AddEdge(&graph.Edge{From: pm.ID, To: parent.ID, Kind: graph.EdgeMemberOf})
	g.AddEdge(&graph.Edge{From: cm.ID, To: child.ID, Kind: graph.EdgeMemberOf})

	res := &semantic.EnrichResult{}
	addOverrideEdges(g, child, parent, "test-lsp", graph.OriginLSPDispatch, res)
	if res.EdgesAdded != 1 {
		t.Fatalf("expected 1 EdgesAdded, got %d", res.EdgesAdded)
	}

	var found *graph.Edge
	for _, e := range g.GetOutEdges(cm.ID) {
		if e.Kind == graph.EdgeOverrides && e.To == pm.ID {
			found = e
		}
	}
	if found == nil {
		t.Fatal("expected EdgeOverrides Child.run → Base.run")
	}
	if found.Origin != graph.OriginLSPDispatch {
		t.Errorf("origin: got %q, want lsp_dispatch", found.Origin)
	}
}

// TestAddOverrideEdges_PromotesExisting confirms the helper promotes
// an existing override from a lower tier when the LSP confirms it.
func TestAddOverrideEdges_PromotesExisting(t *testing.T) {
	g := graph.New()
	parent := &graph.Node{ID: "x.go::P", Kind: graph.KindType, Name: "P", Language: "go"}
	child := &graph.Node{ID: "x.go::C", Kind: graph.KindType, Name: "C", Language: "go"}
	pm := &graph.Node{ID: "x.go::P.f", Kind: graph.KindMethod, Name: "f", Language: "go"}
	cm := &graph.Node{ID: "x.go::C.f", Kind: graph.KindMethod, Name: "f", Language: "go"}
	for _, n := range []*graph.Node{parent, child, pm, cm} {
		g.AddNode(n)
	}
	g.AddEdge(&graph.Edge{From: pm.ID, To: parent.ID, Kind: graph.EdgeMemberOf})
	g.AddEdge(&graph.Edge{From: cm.ID, To: child.ID, Kind: graph.EdgeMemberOf})
	// Pre-existing inferred override.
	g.AddEdge(&graph.Edge{From: cm.ID, To: pm.ID, Kind: graph.EdgeOverrides, Origin: graph.OriginASTInferred})

	res := &semantic.EnrichResult{}
	addOverrideEdges(g, child, parent, "lsp", graph.OriginLSPDispatch, res)
	if res.EdgesAdded != 0 || res.EdgesConfirmed != 1 {
		t.Fatalf("expected 0 added + 1 confirmed; got added=%d confirmed=%d", res.EdgesAdded, res.EdgesConfirmed)
	}
	var got *graph.Edge
	for _, e := range g.GetOutEdges(cm.ID) {
		if e.Kind == graph.EdgeOverrides {
			got = e
		}
	}
	if got == nil || got.Origin != graph.OriginLSPDispatch {
		t.Fatalf("expected promotion to lsp_dispatch; got %v", got)
	}
}

// TestAddOverrideEdges_NoSelfEdge guards against same-method self
// edges when child and parent accidentally point at the same node.
func TestAddOverrideEdges_NoSelfEdge(t *testing.T) {
	g := graph.New()
	t1 := &graph.Node{ID: "x.go::T", Kind: graph.KindType, Name: "T"}
	m := &graph.Node{ID: "x.go::T.f", Kind: graph.KindMethod, Name: "f"}
	g.AddNode(t1)
	g.AddNode(m)
	g.AddEdge(&graph.Edge{From: m.ID, To: t1.ID, Kind: graph.EdgeMemberOf})

	res := &semantic.EnrichResult{}
	addOverrideEdges(g, t1, t1, "lsp", graph.OriginLSPDispatch, res)
	if res.EdgesAdded != 0 {
		t.Fatalf("self-loop should produce no edges, got %d", res.EdgesAdded)
	}
}
