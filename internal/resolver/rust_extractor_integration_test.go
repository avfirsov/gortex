package resolver_test

import (
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser/languages"
	"github.com/zzet/gortex/internal/resolver"
)

func TestRustTraitExtendsGenericDispatchExtractionIntegration(t *testing.T) {
	t.Parallel()

	src := []byte(`
trait Parent {
    fn p(&self);
}

trait Child: Parent + ?Sized + 'static {}

fn call<T: Child>(value: T) {
    value.p();
}
`)
	result, err := languages.NewRustExtractor().Extract("src/lib.rs", src)
	if err != nil {
		t.Fatalf("extract Rust source: %v", err)
	}
	if result.Tree != nil {
		defer result.Tree.Close()
	}

	var parent, child, parentMethod, caller *graph.Node
	for _, node := range result.Nodes {
		switch {
		case node.Kind == graph.KindInterface && node.Name == "Parent":
			parent = node
		case node.Kind == graph.KindInterface && node.Name == "Child":
			child = node
		case node.Kind == graph.KindMethod && node.Name == "p":
			parentMethod = node
		case node.Kind == graph.KindFunction && node.Name == "call":
			caller = node
		}
	}
	if parent == nil || child == nil || parentMethod == nil || caller == nil {
		t.Fatalf("missing extracted symbols: parent=%v child=%v method=%v caller=%v", parent, child, parentMethod, caller)
	}

	var extractedExtends *graph.Edge
	for _, edge := range result.Edges {
		if edge.Kind == graph.EdgeExtends && edge.From == child.ID {
			extractedExtends = edge
			break
		}
	}
	if extractedExtends == nil {
		t.Fatal("extractor did not emit Child extends edge")
	}
	if extractedExtends.To != "unresolved::extends::Parent" {
		t.Fatalf("extracted extends target = %q, want unresolved::extends::Parent", extractedExtends.To)
	}

	var extractedCall *graph.Edge
	for _, edge := range result.Edges {
		if edge.Kind == graph.EdgeCalls && edge.From == caller.ID && strings.HasSuffix(edge.To, ".p") {
			extractedCall = edge
			break
		}
	}
	if extractedCall == nil {
		var calls []string
		for _, edge := range result.Edges {
			if edge.Kind == graph.EdgeCalls {
				calls = append(calls, edge.From+" -> "+edge.To)
			}
		}
		t.Fatalf("extractor did not emit generic value.p() call edge; calls=%v", calls)
	}

	g := graph.New()
	g.AddBatch(result.Nodes, result.Edges)
	if got := resolver.ResolveRustScopeCalls(g); got != 2 {
		t.Fatalf("first resolution count = %d, want 2 (extends + call)", got)
	}

	assertRustEdgeTarget(t, g.GetOutEdges(child.ID), graph.EdgeExtends, parent.ID)
	assertRustEdgeSource(t, g.GetInEdges(parent.ID), graph.EdgeExtends, child.ID)
	assertRustEdgeTarget(t, g.GetOutEdges(caller.ID), graph.EdgeCalls, parentMethod.ID)

	before := g.EdgeCount()
	if got := resolver.ResolveRustScopeCalls(g); got != 0 {
		t.Fatalf("second resolution count = %d, want 0", got)
	}
	if after := g.EdgeCount(); after != before {
		t.Fatalf("second resolution changed edge count: before=%d after=%d", before, after)
	}
	assertRustEdgeTarget(t, g.GetOutEdges(child.ID), graph.EdgeExtends, parent.ID)
	assertRustEdgeTarget(t, g.GetOutEdges(caller.ID), graph.EdgeCalls, parentMethod.ID)
}

func TestRustDuplicateModuleTraitNamesStayIsolated(t *testing.T) {
	t.Parallel()

	type fixture struct {
		path string
		src  string
	}
	fixtures := []fixture{
		{
			path: "src/a.rs",
			src: `trait Parent { fn p(&self); }
trait Child: Parent {}
fn call_a<T: Child>(value: T) { value.p(); }
`,
		},
		{
			path: "src/b.rs",
			src: `trait Parent { fn p(&self); }
trait Child: Parent {}
fn call_b<T: Child>(value: T) { value.p(); }
`,
		},
	}

	g := graph.New()
	for _, fixture := range fixtures {
		result, err := languages.NewRustExtractor().Extract(fixture.path, []byte(fixture.src))
		if err != nil {
			t.Fatalf("extract %s: %v", fixture.path, err)
		}
		if result.Tree != nil {
			defer result.Tree.Close()
		}
		g.AddBatch(result.Nodes, result.Edges)
	}

	var aParent, bParent, aChild, bChild, aMethod, bMethod, aCaller, bCaller *graph.Node
	for _, node := range g.AllNodes() {
		switch {
		case node.FilePath == "src/a.rs" && node.Kind == graph.KindInterface && node.Name == "Parent":
			aParent = node
		case node.FilePath == "src/b.rs" && node.Kind == graph.KindInterface && node.Name == "Parent":
			bParent = node
		case node.FilePath == "src/a.rs" && node.Kind == graph.KindInterface && node.Name == "Child":
			aChild = node
		case node.FilePath == "src/b.rs" && node.Kind == graph.KindInterface && node.Name == "Child":
			bChild = node
		case node.FilePath == "src/a.rs" && node.Kind == graph.KindMethod && node.Name == "p":
			aMethod = node
		case node.FilePath == "src/b.rs" && node.Kind == graph.KindMethod && node.Name == "p":
			bMethod = node
		case node.FilePath == "src/a.rs" && node.Kind == graph.KindFunction && node.Name == "call_a":
			aCaller = node
		case node.FilePath == "src/b.rs" && node.Kind == graph.KindFunction && node.Name == "call_b":
			bCaller = node
		}
	}
	if aParent == nil || bParent == nil || aChild == nil || bChild == nil || aMethod == nil || bMethod == nil || aCaller == nil || bCaller == nil {
		t.Fatalf("missing duplicate-module fixture symbols: aParent=%v bParent=%v aChild=%v bChild=%v aMethod=%v bMethod=%v aCaller=%v bCaller=%v", aParent, bParent, aChild, bChild, aMethod, bMethod, aCaller, bCaller)
	}

	if got := resolver.ResolveRustScopeCalls(g); got != 4 {
		t.Fatalf("duplicate-module resolution count = %d, want 4", got)
	}
	assertRustEdgeTarget(t, g.GetOutEdges(aChild.ID), graph.EdgeExtends, aParent.ID)
	assertRustEdgeTarget(t, g.GetOutEdges(bChild.ID), graph.EdgeExtends, bParent.ID)
	assertRustEdgeTarget(t, g.GetOutEdges(aCaller.ID), graph.EdgeCalls, aMethod.ID)
	assertRustEdgeTarget(t, g.GetOutEdges(bCaller.ID), graph.EdgeCalls, bMethod.ID)
	assertRustNoEdgeTarget(t, g.GetOutEdges(aCaller.ID), graph.EdgeCalls, bMethod.ID)
	assertRustNoEdgeTarget(t, g.GetOutEdges(bCaller.ID), graph.EdgeCalls, aMethod.ID)
	if got := resolver.ResolveRustScopeCalls(g); got != 0 {
		t.Fatalf("duplicate-module second resolution count = %d, want 0", got)
	}
}

func TestRustExternalQualifiedBoundDoesNotBindLocalBasename(t *testing.T) {
	t.Parallel()

	src := []byte(`trait Matcher { fn find(&self); }
fn call<T: external::Matcher>(value: T) { value.find(); }
`)
	result, err := languages.NewRustExtractor().Extract("src/lib.rs", src)
	if err != nil {
		t.Fatalf("extract external-bound fixture: %v", err)
	}
	if result.Tree != nil {
		defer result.Tree.Close()
	}
	var caller *graph.Node
	for _, node := range result.Nodes {
		if node.Kind == graph.KindFunction && node.Name == "call" {
			caller = node
			break
		}
	}
	if caller == nil {
		t.Fatal("missing external-bound caller")
	}
	var call *graph.Edge
	for _, edge := range result.Edges {
		if edge.Kind == graph.EdgeCalls && edge.From == caller.ID {
			call = edge
			break
		}
	}
	if call == nil {
		t.Fatal("missing external-bound call edge")
	}
	originalTarget := call.To

	g := graph.New()
	g.AddBatch(result.Nodes, result.Edges)
	if got := resolver.ResolveRustScopeCalls(g); got != 0 {
		t.Fatalf("external-bound resolution count = %d, want 0", got)
	}
	if call.To != originalTarget || !graph.IsUnresolvedTarget(call.To) {
		t.Fatalf("external::Matcher call resolved to local symbol: before=%q after=%q", originalTarget, call.To)
	}
}

func TestRustMarkerTraitExtendsResolvesAndCounts(t *testing.T) {
	t.Parallel()

	parent := &graph.Node{
		ID: "src/lib.rs::Parent", Kind: graph.KindInterface, Name: "Parent",
		FilePath: "src/lib.rs", Language: "rust", RepoPrefix: "repo",
	}
	child := &graph.Node{
		ID: "src/lib.rs::Child", Kind: graph.KindInterface, Name: "Child",
		FilePath: "src/lib.rs", Language: "rust", RepoPrefix: "repo",
	}
	extends := &graph.Edge{
		From: child.ID, To: "unresolved::extends::Parent", Kind: graph.EdgeExtends,
		FilePath: child.FilePath, Meta: map[string]any{"rust_trait_path": "Parent"},
	}
	g := graph.New()
	g.AddBatch([]*graph.Node{parent, child}, []*graph.Edge{extends})

	if got := resolver.ResolveRustScopeCalls(g); got != 1 {
		t.Fatalf("marker-trait resolution count = %d, want 1", got)
	}
	assertRustEdgeTarget(t, g.GetOutEdges(child.ID), graph.EdgeExtends, parent.ID)
	assertRustEdgeSource(t, g.GetInEdges(parent.ID), graph.EdgeExtends, child.ID)
	if got := resolver.ResolveRustScopeCalls(g); got != 0 {
		t.Fatalf("marker-trait second resolution count = %d, want 0", got)
	}
}

func assertRustEdgeTarget(t *testing.T, edges []*graph.Edge, kind graph.EdgeKind, target string) {
	t.Helper()
	for _, edge := range edges {
		if edge.Kind == kind && edge.To == target {
			return
		}
	}
	t.Fatalf("missing %s edge to %q in %#v", kind, target, edges)
}

func assertRustNoEdgeTarget(t *testing.T, edges []*graph.Edge, kind graph.EdgeKind, target string) {
	t.Helper()
	for _, edge := range edges {
		if edge.Kind == kind && edge.To == target {
			t.Fatalf("unexpected %s edge to %q in %#v", kind, target, edges)
		}
	}
}

func assertRustEdgeSource(t *testing.T, edges []*graph.Edge, kind graph.EdgeKind, source string) {
	t.Helper()
	for _, edge := range edges {
		if edge.Kind == kind && edge.From == source {
			return
		}
	}
	t.Fatalf("missing %s edge from %q in %#v", kind, source, edges)
}
