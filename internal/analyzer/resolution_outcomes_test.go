package analyzer_test

// PURPOSE — shape tests for AnalyzeResolutionOutcomes: verify the function
// classifies unresolved edges correctly and returns the right struct shape.
// RATIONALE — tests are MCP-layer-free so the core logic is independently
// verifiable; they mirror the taxonomy asserted in the MCP-layer tests.
// KEYWORDS — resolution_outcomes, unit, shape

import (
	"testing"

	"github.com/zzet/gortex/internal/analyzer"
	"github.com/zzet/gortex/internal/graph"
)

func TestAnalyzeResolutionOutcomes_Shape(t *testing.T) {
	g := newTestGraph()
	// caller (go)
	g.AddNode(&graph.Node{ID: "a.go::caller", Kind: graph.KindFunction, Name: "caller", FilePath: "a.go", Language: "go"})

	// unresolved edge — no definition in graph at all.
	g.AddEdge(&graph.Edge{From: "a.go::caller", To: "unresolved::ghost", Kind: graph.EdgeCalls, FilePath: "a.go", Line: 5})

	res := analyzer.AnalyzeResolutionOutcomes(g, "", 50)
	if res.Total == 0 {
		t.Fatal("expected total > 0")
	}
	if res.Rows == nil {
		t.Fatal("expected rows not nil")
	}
	if len(res.Rows) == 0 {
		t.Fatal("expected at least one row")
	}
}

func TestAnalyzeResolutionOutcomes_Taxonomy(t *testing.T) {
	g := newTestGraph()
	g.AddNode(&graph.Node{ID: "a.go::caller", Kind: graph.KindFunction, Name: "caller", FilePath: "a.go", Language: "go"})

	// ambiguous_multi_match: two same-name go funcs named "doThing".
	g.AddNode(&graph.Node{ID: "x.go::doThing", Kind: graph.KindFunction, Name: "doThing", FilePath: "x.go", Language: "go"})
	g.AddNode(&graph.Node{ID: "y.go::doThing", Kind: graph.KindFunction, Name: "doThing", FilePath: "y.go", Language: "go"})
	g.AddEdge(&graph.Edge{From: "a.go::caller", To: "unresolved::doThing", Kind: graph.EdgeCalls, FilePath: "a.go", Line: 2})

	// candidate_out_of_scope: exactly one same-lang def named "single".
	g.AddNode(&graph.Node{ID: "z.go::single", Kind: graph.KindFunction, Name: "single", FilePath: "z.go", Language: "go"})
	g.AddEdge(&graph.Edge{From: "a.go::caller", To: "unresolved::single", Kind: graph.EdgeCalls, FilePath: "a.go", Line: 3})

	// cross_language_only: only a python def named "pyOnly".
	g.AddNode(&graph.Node{ID: "p.py::pyOnly", Kind: graph.KindFunction, Name: "pyOnly", FilePath: "p.py", Language: "python"})
	g.AddEdge(&graph.Edge{From: "a.go::caller", To: "unresolved::pyOnly", Kind: graph.EdgeCalls, FilePath: "a.go", Line: 4})

	// no_definition: nothing named "ghost".
	g.AddEdge(&graph.Edge{From: "a.go::caller", To: "unresolved::ghost", Kind: graph.EdgeCalls, FilePath: "a.go", Line: 5})

	res := analyzer.AnalyzeResolutionOutcomes(g, "", 50)
	check := func(reason string, want int) {
		t.Helper()
		got := res.ByReason[reason]
		if got != want {
			t.Errorf("by_reason[%q] = %d, want %d", reason, got, want)
		}
	}
	check("ambiguous_multi_match", 1)
	check("candidate_out_of_scope", 1)
	check("cross_language_only", 1)
	check("no_definition", 1)
}

func TestAnalyzeResolutionOutcomes_ReasonFilter(t *testing.T) {
	g := newTestGraph()
	g.AddNode(&graph.Node{ID: "a.go::caller", Kind: graph.KindFunction, Name: "caller", FilePath: "a.go", Language: "go"})
	g.AddEdge(&graph.Edge{From: "a.go::caller", To: "unresolved::ghost", Kind: graph.EdgeCalls, FilePath: "a.go", Line: 5})

	res := analyzer.AnalyzeResolutionOutcomes(g, "no_definition", 50)
	if len(res.Rows) != 1 {
		t.Fatalf("reason filter: want 1 row, got %d", len(res.Rows))
	}
	if res.Rows[0].Reason != "no_definition" {
		t.Errorf("row reason = %q", res.Rows[0].Reason)
	}
}

func TestClassifyUnresolved_NoDefinition(t *testing.T) {
	g := newTestGraph()
	reason, candidates := analyzer.ClassifyUnresolved(g, "ghost", "go")
	if reason != "no_definition" {
		t.Errorf("expected no_definition, got %q", reason)
	}
	if candidates != 0 {
		t.Errorf("expected 0 candidates, got %d", candidates)
	}
}
