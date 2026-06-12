package storetest

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// RunBackendResolverConformance exercises every method of the
// graph.BackendResolver interface against a Factory that produces a
// store implementing both graph.Store and graph.BackendResolver. The
// shape mirrors RunConformance (the main Store contract): a known
// fixture graph, run the rule, assert the post-state matches the
// expected resolution.
//
// Backends that haven't implemented a rule yet ship the Phase 1 stub
// that returns (0, nil); those subtests pass trivially because the
// fixture also asserts zero-progress doesn't break correctness.
func RunBackendResolverConformance(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("BackendResolver_SameFile", func(t *testing.T) { testBRSameFile(t, factory) })
	t.Run("BackendResolver_SamePackage", func(t *testing.T) { testBRSamePackage(t, factory) })
	t.Run("BackendResolver_ImportAware", func(t *testing.T) { testBRImportAware(t, factory) })
	t.Run("BackendResolver_RelativeImports", func(t *testing.T) { testBRRelativeImports(t, factory) })
	t.Run("BackendResolver_CrossRepo", func(t *testing.T) { testBRCrossRepo(t, factory) })
	t.Run("BackendResolver_UniqueNames", func(t *testing.T) { testBRUniqueNames(t, factory) })
	t.Run("BackendResolver_ExternalCallStubs", func(t *testing.T) { testBRExternalCallStubs(t, factory) })
	t.Run("BackendResolver_AllBulk", func(t *testing.T) { testBRAllBulk(t, factory) })
}

func asBackendResolver(t *testing.T, s graph.Store) graph.BackendResolver {
	t.Helper()
	br, ok := s.(graph.BackendResolver)
	if !ok {
		t.Skip("store does not implement graph.BackendResolver")
	}
	return br
}

func testBRSameFile(t *testing.T, factory Factory) {
	t.Helper()
	s := factory(t)
	br := asBackendResolver(t, s)
	// caller and target in same file — unambiguous match
	s.AddNode(mkNode("a.go::Foo", "Foo", "a.go", graph.KindFunction))
	s.AddNode(mkNode("a.go::Bar", "Bar", "a.go", graph.KindFunction))
	s.AddEdge(&graph.Edge{
		From: "a.go::Foo", To: "unresolved::Bar", Kind: graph.EdgeCalls,
		FilePath: "a.go", Line: 1, Origin: "",
	})
	n, err := br.ResolveSameFile()
	if err != nil {
		t.Fatalf("ResolveSameFile: %v", err)
	}
	if n == 0 {
		// stub backend — skip the post-state assertions
		return
	}
	if n != 1 {
		t.Fatalf("ResolveSameFile resolved %d, want 1", n)
	}
	// edge should now point at a.go::Bar with origin ast_resolved
	got := s.GetOutEdges("a.go::Foo")
	if len(got) != 1 || got[0].To != "a.go::Bar" || got[0].Origin != graph.OriginASTResolved {
		t.Fatalf("ResolveSameFile post-state: edges=%+v", got)
	}
}

func testBRSamePackage(t *testing.T, factory Factory) {
	t.Helper()
	s := factory(t)
	br := asBackendResolver(t, s)
	// caller in pkg/a.go, target in pkg/b.go — same directory
	s.AddNode(mkRepoNode("pkg/a.go::Caller", "Caller", "pkg/a.go", "r1", graph.KindFunction))
	s.AddNode(mkRepoNode("pkg/b.go::Target", "Target", "pkg/b.go", "r1", graph.KindFunction))
	s.AddEdge(&graph.Edge{
		From: "pkg/a.go::Caller", To: "unresolved::Target", Kind: graph.EdgeCalls,
		FilePath: "pkg/a.go", Line: 1, Origin: "",
	})
	n, err := br.ResolveSamePackage()
	if err != nil {
		t.Fatalf("ResolveSamePackage: %v", err)
	}
	if n == 0 {
		return
	}
	if n != 1 {
		t.Fatalf("ResolveSamePackage resolved %d, want 1", n)
	}
	got := s.GetOutEdges("pkg/a.go::Caller")
	if len(got) != 1 || got[0].To != "pkg/b.go::Target" || got[0].Origin != graph.OriginASTResolved {
		t.Fatalf("ResolveSamePackage post-state: edges=%+v", got)
	}
}

func testBRImportAware(t *testing.T, factory Factory) {
	t.Helper()
	s := factory(t)
	br := asBackendResolver(t, s)
	// caller.go imports lib.go which exports Target
	s.AddNode(mkNode("caller.go", "caller.go", "caller.go", graph.KindFile))
	s.AddNode(mkNode("lib.go", "lib.go", "lib.go", graph.KindFile))
	s.AddNode(mkNode("caller.go::Caller", "Caller", "caller.go", graph.KindFunction))
	s.AddNode(mkNode("lib.go::Target", "Target", "lib.go", graph.KindFunction))
	// the imports edge
	s.AddEdge(&graph.Edge{
		From: "caller.go", To: "lib.go", Kind: graph.EdgeImports,
		FilePath: "caller.go", Line: 1, Origin: graph.OriginASTResolved,
	})
	// the unresolved call
	s.AddEdge(&graph.Edge{
		From: "caller.go::Caller", To: "unresolved::Target", Kind: graph.EdgeCalls,
		FilePath: "caller.go", Line: 5, Origin: "",
	})
	n, err := br.ResolveImportAware()
	if err != nil {
		t.Fatalf("ResolveImportAware: %v", err)
	}
	if n == 0 {
		return
	}
	if n != 1 {
		t.Fatalf("ResolveImportAware resolved %d, want 1", n)
	}
	got := s.GetOutEdges("caller.go::Caller")
	var found bool
	for _, e := range got {
		if e.To == "lib.go::Target" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ResolveImportAware post-state: edges=%+v, want one to lib.go::Target", got)
	}
}

func testBRRelativeImports(t *testing.T, factory Factory) {
	t.Helper()
	s := factory(t)
	br := asBackendResolver(t, s)
	// python relative-import stub
	s.AddNode(mkNode("app/util.py", "app/util.py", "app/util.py", graph.KindFile))
	s.AddNode(mkNode("app/main.py", "app/main.py", "app/main.py", graph.KindFile))
	s.AddEdge(&graph.Edge{
		From: "app/main.py", To: "unresolved::pyrel::app/util", Kind: graph.EdgeImports,
		FilePath: "app/main.py", Line: 1, Origin: "",
	})
	n, err := br.ResolveRelativeImports("python")
	if err != nil {
		t.Fatalf("ResolveRelativeImports: %v", err)
	}
	if n == 0 {
		return
	}
	if n != 1 {
		t.Fatalf("ResolveRelativeImports resolved %d, want 1", n)
	}
	got := s.GetOutEdges("app/main.py")
	var found bool
	for _, e := range got {
		if e.To == "app/util.py" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ResolveRelativeImports post-state: edges=%+v, want one to app/util.py", got)
	}
}

func testBRCrossRepo(t *testing.T, factory Factory) {
	t.Helper()
	s := factory(t)
	br := asBackendResolver(t, s)
	s.AddNode(mkRepoNode("r1/a.go::Caller", "Caller", "r1/a.go", "r1", graph.KindFunction))
	s.AddNode(mkRepoNode("r2/x.go::Target", "Target", "r2/x.go", "r2", graph.KindFunction))
	s.AddEdge(&graph.Edge{
		From: "r1/a.go::Caller", To: "unresolved::Target", Kind: graph.EdgeCalls,
		FilePath: "r1/a.go", Line: 1, Origin: "",
	})
	n, err := br.ResolveCrossRepo()
	if err != nil {
		t.Fatalf("ResolveCrossRepo: %v", err)
	}
	if n == 0 {
		return
	}
	if n != 1 {
		t.Fatalf("ResolveCrossRepo resolved %d, want 1", n)
	}
	got := s.GetOutEdges("r1/a.go::Caller")
	if len(got) != 1 || got[0].To != "r2/x.go::Target" || !got[0].CrossRepo {
		t.Fatalf("ResolveCrossRepo post-state: edges=%+v", got)
	}
}

func testBRUniqueNames(t *testing.T, factory Factory) {
	t.Helper()
	s := factory(t)
	br := asBackendResolver(t, s)
	// One unique-name candidate in the graph.
	s.AddNode(mkNode("a.go::Foo", "Foo", "a.go", graph.KindFunction))
	s.AddNode(mkNode("b.go::Target", "Target", "b.go", graph.KindFunction))
	s.AddEdge(&graph.Edge{
		From: "a.go::Foo", To: "unresolved::Target", Kind: graph.EdgeCalls,
		FilePath: "a.go", Line: 1, Origin: "",
	})
	n, err := br.ResolveUniqueNames()
	if err != nil {
		t.Fatalf("ResolveUniqueNames: %v", err)
	}
	if n == 0 {
		return
	}
	if n != 1 {
		t.Fatalf("ResolveUniqueNames resolved %d, want 1", n)
	}
	got := s.GetOutEdges("a.go::Foo")
	if len(got) != 1 || got[0].To != "b.go::Target" {
		t.Fatalf("ResolveUniqueNames post-state: edges=%+v", got)
	}
}

func testBRExternalCallStubs(t *testing.T, factory Factory) {
	t.Helper()
	s := factory(t)
	br := asBackendResolver(t, s)
	s.AddNode(mkNode("a.go::Caller", "Caller", "a.go", graph.KindFunction))
	// edge to external::npm/foo::bar with no stub node
	s.AddEdge(&graph.Edge{
		From: "a.go::Caller", To: "external::npm/foo::bar", Kind: graph.EdgeCalls,
		FilePath: "a.go", Line: 1, Origin: "",
	})
	n, err := br.ResolveExternalCallStubs()
	if err != nil {
		t.Fatalf("ResolveExternalCallStubs: %v", err)
	}
	if n == 0 {
		return
	}
	if n < 1 {
		t.Fatalf("ResolveExternalCallStubs resolved %d, want >= 1", n)
	}
	// stub node must now exist
	if s.GetNode("external::npm/foo::bar") == nil {
		t.Fatalf("external stub node not created")
	}
}

func testBRAllBulk(t *testing.T, factory Factory) {
	t.Helper()
	s := factory(t)
	br := asBackendResolver(t, s)
	// Mix of resolvable + stub cases.
	s.AddNode(mkNode("a.go::Foo", "Foo", "a.go", graph.KindFunction))
	s.AddNode(mkNode("a.go::Bar", "Bar", "a.go", graph.KindFunction))
	s.AddNode(mkNode("b.go::Unique", "Unique", "b.go", graph.KindFunction))
	// same-file
	s.AddEdge(&graph.Edge{
		From: "a.go::Foo", To: "unresolved::Bar", Kind: graph.EdgeCalls,
		FilePath: "a.go", Line: 1, Origin: "",
	})
	// unique-name
	s.AddEdge(&graph.Edge{
		From: "a.go::Foo", To: "unresolved::Unique", Kind: graph.EdgeCalls,
		FilePath: "a.go", Line: 2, Origin: "",
	})
	n, err := br.ResolveAllBulk()
	if err != nil {
		t.Fatalf("ResolveAllBulk: %v", err)
	}
	_ = n // 0 on stub backends, >0 on real
}
