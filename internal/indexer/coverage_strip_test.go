package indexer

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// TestStripFunctionShape covers the gate-off path of
// applyCoverageDomains: when CoverageConfig.FunctionShape is
// disabled, the strip pass drops param/closure/generic_param nodes
// plus their associated edges, leaving the function/method/file
// nodes and their edges untouched.
func TestStripFunctionShape(t *testing.T) {
	result := &parser.ExtractionResult{
		Nodes: []*graph.Node{
			{ID: "f::Run", Kind: graph.KindFunction, Name: "Run"},
			{ID: "f::Run#param:x", Kind: graph.KindParam, Name: "x"},
			{ID: "f::Run#closure@5", Kind: graph.KindClosure, Name: "closure@5"},
			{ID: "f::Run#tparam:T", Kind: graph.KindGenericParam, Name: "T"},
			{ID: "pkg/f.go", Kind: graph.KindFile, Name: "f.go"},
		},
		Edges: []*graph.Edge{
			{From: "pkg/f.go", To: "f::Run", Kind: graph.EdgeDefines},
			{From: "f::Run#param:x", To: "f::Run", Kind: graph.EdgeParamOf},
			{From: "f::Run#param:x", To: "unresolved::int", Kind: graph.EdgeTypedAs},
			{From: "f::Run", To: "unresolved::error", Kind: graph.EdgeReturns},
			{From: "f::Run#closure@5", To: "f::Run", Kind: graph.EdgeMemberOf},
			{From: "f::Run#tparam:T", To: "f::Run", Kind: graph.EdgeMemberOf},
		},
	}
	stripFunctionShape(result)

	for _, n := range result.Nodes {
		switch n.Kind {
		case graph.KindParam, graph.KindClosure, graph.KindGenericParam:
			t.Errorf("node %q (kind %s) should have been stripped", n.ID, n.Kind)
		}
	}
	if len(result.Nodes) != 2 {
		t.Errorf("expected 2 nodes left (function + file), got %d", len(result.Nodes))
	}
	for _, e := range result.Edges {
		switch e.Kind {
		case graph.EdgeParamOf, graph.EdgeReturns, graph.EdgeTypedAs, graph.EdgeCaptures:
			t.Errorf("edge %s -> %s (kind %s) should have been stripped", e.From, e.To, e.Kind)
		}
	}
	// EdgeMemberOf edges that pointed FROM stripped nodes should
	// also be dropped, even though EdgeMemberOf is not itself a
	// function-shape edge — strips are endpoint-aware.
	for _, e := range result.Edges {
		if e.From == "f::Run#closure@5" || e.From == "f::Run#tparam:T" {
			t.Errorf("edge from stripped node still present: %+v", e)
		}
	}
	// The defines edge should survive.
	hasDefines := false
	for _, e := range result.Edges {
		if e.Kind == graph.EdgeDefines {
			hasDefines = true
		}
	}
	if !hasDefines {
		t.Error("defines edge missing after strip")
	}
}
