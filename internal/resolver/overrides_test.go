package resolver

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// TestInferOverrides_MatchesByMethodName builds a small graph with
// child & parent classes, then asserts that InferOverrides materialises
// EdgeOverrides for each child method whose name matches a parent
// method.
func TestInferOverrides_MatchesByMethodName(t *testing.T) {
	g := graph.New()

	parent := &graph.Node{ID: "p.ts::Animal", Kind: graph.KindType, Name: "Animal", Language: "typescript"}
	child := &graph.Node{ID: "p.ts::Dog", Kind: graph.KindType, Name: "Dog", Language: "typescript"}
	parentSpeak := &graph.Node{ID: "p.ts::Animal.speak", Kind: graph.KindMethod, Name: "speak", Language: "typescript", FilePath: "p.ts", StartLine: 5}
	parentEat := &graph.Node{ID: "p.ts::Animal.eat", Kind: graph.KindMethod, Name: "eat", Language: "typescript", FilePath: "p.ts", StartLine: 8}
	childSpeak := &graph.Node{ID: "p.ts::Dog.speak", Kind: graph.KindMethod, Name: "speak", Language: "typescript", FilePath: "p.ts", StartLine: 15}
	childRun := &graph.Node{ID: "p.ts::Dog.run", Kind: graph.KindMethod, Name: "run", Language: "typescript", FilePath: "p.ts", StartLine: 18}

	for _, n := range []*graph.Node{parent, child, parentSpeak, parentEat, childSpeak, childRun} {
		g.AddNode(n)
	}
	// Membership.
	g.AddEdge(&graph.Edge{From: parentSpeak.ID, To: parent.ID, Kind: graph.EdgeMemberOf, FilePath: "p.ts", Line: 5})
	g.AddEdge(&graph.Edge{From: parentEat.ID, To: parent.ID, Kind: graph.EdgeMemberOf, FilePath: "p.ts", Line: 8})
	g.AddEdge(&graph.Edge{From: childSpeak.ID, To: child.ID, Kind: graph.EdgeMemberOf, FilePath: "p.ts", Line: 15})
	g.AddEdge(&graph.Edge{From: childRun.ID, To: child.ID, Kind: graph.EdgeMemberOf, FilePath: "p.ts", Line: 18})
	// Hierarchy edge.
	g.AddEdge(&graph.Edge{From: child.ID, To: parent.ID, Kind: graph.EdgeExtends, FilePath: "p.ts", Line: 10, Origin: graph.OriginASTResolved})

	r := New(g)
	added := r.InferOverrides()
	if added != 1 {
		t.Fatalf("expected 1 override, got %d", added)
	}

	// Walk overrides edges from the child speak method and check it
	// targets the parent speak method.
	out := g.GetOutEdges(childSpeak.ID)
	var found bool
	for _, e := range out {
		if e.Kind == graph.EdgeOverrides && e.To == parentSpeak.ID {
			found = true
			if e.Origin != graph.OriginASTResolved {
				t.Errorf("expected origin ast_resolved, got %q", e.Origin)
			}
		}
	}
	if !found {
		t.Fatal("expected EdgeOverrides Dog.speak → Animal.speak")
	}

	// The only-in-child method should have no override.
	for _, e := range g.GetOutEdges(childRun.ID) {
		if e.Kind == graph.EdgeOverrides {
			t.Fatalf("Dog.run should not override anything; got %v", e)
		}
	}
}

// TestInferOverrides_RespectsMinTier promotes inferred edges to the
// appropriate origin based on the parent edge.
func TestInferOverrides_RespectsMinTier(t *testing.T) {
	g := graph.New()
	parent := &graph.Node{ID: "i.ts::I", Kind: graph.KindInterface, Name: "I", Language: "typescript"}
	child := &graph.Node{ID: "i.ts::C", Kind: graph.KindType, Name: "C", Language: "typescript"}
	pm := &graph.Node{ID: "i.ts::I.do", Kind: graph.KindMethod, Name: "do", Language: "typescript", FilePath: "i.ts", StartLine: 1}
	cm := &graph.Node{ID: "i.ts::C.do", Kind: graph.KindMethod, Name: "do", Language: "typescript", FilePath: "i.ts", StartLine: 5}
	for _, n := range []*graph.Node{parent, child, pm, cm} {
		g.AddNode(n)
	}
	g.AddEdge(&graph.Edge{From: pm.ID, To: parent.ID, Kind: graph.EdgeMemberOf})
	g.AddEdge(&graph.Edge{From: cm.ID, To: child.ID, Kind: graph.EdgeMemberOf})
	// Implements with a heuristic origin.
	g.AddEdge(&graph.Edge{From: child.ID, To: parent.ID, Kind: graph.EdgeImplements, Origin: graph.OriginASTInferred})

	r := New(g)
	added := r.InferOverrides()
	if added != 1 {
		t.Fatalf("added=%d", added)
	}
	for _, e := range g.GetOutEdges(cm.ID) {
		if e.Kind == graph.EdgeOverrides {
			if e.Origin != graph.OriginASTInferred {
				t.Errorf("expected ast_inferred, got %q", e.Origin)
			}
		}
	}
}

// TestInferOverrides_Idempotent runs the pass twice and confirms no
// duplicate edges are emitted.
func TestInferOverrides_Idempotent(t *testing.T) {
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
	g.AddEdge(&graph.Edge{From: child.ID, To: parent.ID, Kind: graph.EdgeExtends, Origin: graph.OriginASTResolved})

	r := New(g)
	if added := r.InferOverrides(); added != 1 {
		t.Fatalf("first run added=%d", added)
	}
	if added := r.InferOverrides(); added != 0 {
		t.Fatalf("second run should add 0, got %d", added)
	}
}
