package analysis

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// TestComputeHITS_ProvenanceAttenuatesLSP builds two authorities with
// identical in-degree, one reached by direct ast_resolved calls and
// one by abundant lsp_dispatch edges. Provenance weighting must give
// the ast-resolved authority the higher score.
func TestComputeHITS_ProvenanceAttenuatesLSP(t *testing.T) {
	g := graph.New()
	for _, id := range []string{"astAuth", "lspAuth", "h1", "h2", "h3", "h4"} {
		g.AddNode(&graph.Node{ID: id, Name: id, Kind: graph.KindFunction})
	}
	g.AddEdge(&graph.Edge{From: "h1", To: "astAuth", Kind: graph.EdgeCalls, Origin: graph.OriginASTResolved})
	g.AddEdge(&graph.Edge{From: "h2", To: "astAuth", Kind: graph.EdgeCalls, Origin: graph.OriginASTResolved})
	g.AddEdge(&graph.Edge{From: "h3", To: "lspAuth", Kind: graph.EdgeCalls, Origin: graph.OriginLSPDispatch})
	g.AddEdge(&graph.Edge{From: "h4", To: "lspAuth", Kind: graph.EdgeCalls, Origin: graph.OriginLSPDispatch})

	res := ComputeHITS(g)
	if !(res.AuthorityOf("astAuth") > res.AuthorityOf("lspAuth")) {
		t.Errorf("ast-resolved authority (%v) must outrank lsp-dispatch authority (%v)",
			res.AuthorityOf("astAuth"), res.AuthorityOf("lspAuth"))
	}
}

func TestComputePageRank_ProvenanceAttenuatesLSP(t *testing.T) {
	// A single hub calls two leaves: one via a direct ast_resolved
	// edge, one via an abundant lsp_dispatch edge. PageRank conserves
	// mass per source, so the hub's score flows preferentially to the
	// higher-provenance target — the ast leaf must outrank the lsp leaf.
	g := graph.New()
	for _, id := range []string{"hub", "astLeaf", "lspLeaf"} {
		g.AddNode(&graph.Node{ID: id, Name: id, Kind: graph.KindFunction})
	}
	g.AddEdge(&graph.Edge{From: "hub", To: "astLeaf", Kind: graph.EdgeCalls, Origin: graph.OriginASTResolved})
	g.AddEdge(&graph.Edge{From: "hub", To: "lspLeaf", Kind: graph.EdgeCalls, Origin: graph.OriginLSPDispatch})

	res := ComputePageRank(g)
	if !(res.ScoreOf("astLeaf") > res.ScoreOf("lspLeaf")) {
		t.Errorf("ast-resolved target (%v) must outrank lsp-dispatch target (%v)",
			res.ScoreOf("astLeaf"), res.ScoreOf("lspLeaf"))
	}
}
