package rerank

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestProvenanceSignal_RewardsAstResolvedOverLSP(t *testing.T) {
	g := newTestGraph()
	astNode := mustNode(g, "f.go::AstAuthority", "AstAuthority", graph.KindFunction)
	lspNode := mustNode(g, "f.go::LspAuthority", "LspAuthority", graph.KindFunction)
	g.AddEdge(&graph.Edge{From: "c1", To: astNode.ID, Kind: graph.EdgeCalls, Origin: graph.OriginASTResolved})
	g.AddEdge(&graph.Edge{From: "c2", To: astNode.ID, Kind: graph.EdgeCalls, Origin: graph.OriginASTResolved})
	g.AddEdge(&graph.Edge{From: "c3", To: lspNode.ID, Kind: graph.EdgeCalls, Origin: graph.OriginLSPDispatch})
	g.AddEdge(&graph.Edge{From: "c4", To: lspNode.ID, Kind: graph.EdgeCalls, Origin: graph.OriginLSPDispatch})

	cands := []*Candidate{candidateFor(astNode, 0, -1), candidateFor(lspNode, 1, -1)}
	ctx := &Context{Graph: g}
	ctx.prepare(cands)
	sig := ProvenanceSignal{}

	astScore := sig.Contribute("q", cands[0], ctx)
	lspScore := sig.Contribute("q", cands[1], ctx)
	if !(astScore > lspScore) {
		t.Errorf("ast-resolved authority (%v) must score above lsp-dispatch (%v)", astScore, lspScore)
	}
	if astScore != 1.0 {
		t.Errorf("ast-resolved-only in-edges should score 1.0, got %v", astScore)
	}
}

func TestProvenanceSignal_NoInboundEdgesZero(t *testing.T) {
	g := newTestGraph()
	n := mustNode(g, "f.go::Leaf", "Leaf", graph.KindFunction)
	c := candidateFor(n, 0, -1)
	ctx := &Context{Graph: g}
	ctx.prepare([]*Candidate{c})
	if got := (ProvenanceSignal{}).Contribute("q", c, ctx); got != 0 {
		t.Errorf("no inbound edges should score 0, got %v", got)
	}
}

func TestProvenanceSignal_NilSafety(t *testing.T) {
	sig := ProvenanceSignal{}
	if got := sig.Contribute("q", nil, &Context{}); got != 0 {
		t.Errorf("nil candidate = %v, want 0", got)
	}
	if got := sig.Contribute("q", &Candidate{Node: &graph.Node{ID: "x"}}, nil); got != 0 {
		t.Errorf("nil ctx = %v, want 0", got)
	}
}
