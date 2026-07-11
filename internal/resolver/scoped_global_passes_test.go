package resolver

import (
	"iter"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// countingStore wraps a graph.Store and counts the read paths the scoped global
// passes take, so a test can prove a scoped run drives off the per-repo readers
// (GetRepoEdges / GetRepoNodes) instead of the whole-graph scans and materialises
// fewer nodes. It deliberately does NOT re-expose the optional capabilities
// (FnValuePlaceholderScanner, NodesByKindsScanner, …) of the wrapped store, so
// both the scoped and unscoped runs under test take the same generic fallback
// paths and the comparison isolates the scope effect.
type countingStore struct {
	graph.Store
	edgesByKind   int
	repoEdges     map[string]int
	nodesReturned int // total *Node materialised via node-returning reads
}

func newCountingStore(s graph.Store) *countingStore {
	return &countingStore{Store: s, repoEdges: map[string]int{}}
}

func (c *countingStore) EdgesByKind(k graph.EdgeKind) iter.Seq[*graph.Edge] {
	c.edgesByKind++
	return c.Store.EdgesByKind(k)
}

func (c *countingStore) GetRepoEdges(prefix string) []*graph.Edge {
	c.repoEdges[prefix]++
	return c.Store.GetRepoEdges(prefix)
}

func (c *countingStore) GetFileNodes(path string) []*graph.Node {
	ns := c.Store.GetFileNodes(path)
	c.nodesReturned += len(ns)
	return ns
}

func (c *countingStore) FindNodesByName(name string) []*graph.Node {
	ns := c.Store.FindNodesByName(name)
	c.nodesReturned += len(ns)
	return ns
}

func (c *countingStore) NodesByKind(k graph.NodeKind) iter.Seq[*graph.Node] {
	inner := c.Store.NodesByKind(k)
	return func(yield func(*graph.Node) bool) {
		for n := range inner {
			c.nodesReturned++
			if !yield(n) {
				return
			}
		}
	}
}

func overrideEdgeSet(g graph.Store) map[string]bool {
	out := map[string]bool{}
	for _, e := range g.AllEdges() {
		if e.Kind == graph.EdgeOverrides {
			out[e.From+"->"+e.To] = true
		}
	}
	return out
}

// crossRepoOverrideFixture builds a child type in repo A whose supertype lives
// in the (unchanged) repo B, so the override edge Derived.Do -> Base.Do spans a
// repo boundary. InferOverrides has no same-repo gate, so this pair is real.
func crossRepoOverrideFixture() *graph.Graph {
	g := graph.New()
	// Repo B (unchanged): the parent.
	g.AddNode(&graph.Node{ID: "B::b.go::Base", Kind: graph.KindType, Name: "Base", RepoPrefix: "B", FilePath: "b.go"})
	g.AddNode(&graph.Node{ID: "B::b.go::Base.Do", Kind: graph.KindMethod, Name: "Do", RepoPrefix: "B", FilePath: "b.go"})
	g.AddEdge(&graph.Edge{From: "B::b.go::Base.Do", To: "B::b.go::Base", Kind: graph.EdgeMemberOf})
	// Repo A (changed): the child extending the cross-repo parent.
	g.AddNode(&graph.Node{ID: "A::a.go::Derived", Kind: graph.KindType, Name: "Derived", RepoPrefix: "A", FilePath: "a.go"})
	g.AddNode(&graph.Node{ID: "A::a.go::Derived.Do", Kind: graph.KindMethod, Name: "Do", RepoPrefix: "A", FilePath: "a.go"})
	g.AddEdge(&graph.Edge{From: "A::a.go::Derived.Do", To: "A::a.go::Derived", Kind: graph.EdgeMemberOf})
	g.AddEdge(&graph.Edge{From: "A::a.go::Derived", To: "B::b.go::Base", Kind: graph.EdgeExtends, Origin: graph.OriginASTResolved})
	return g
}

// TestInferOverridesScoped_CrossRepoBoundary asserts a scope that names ONLY the
// changed repo's types still re-derives a cross-repo override whose parent lives
// in an unchanged repo — the filter keeps a parent-edge row when EITHER endpoint
// is in scope, so the changed child is enough.
func TestInferOverridesScoped_CrossRepoBoundary(t *testing.T) {
	full := crossRepoOverrideFixture()
	New(full).InferOverrides()
	want := overrideEdgeSet(full)
	if len(want) != 1 {
		t.Fatalf("setup: expected 1 cross-repo override from full pass, got %d: %v", len(want), want)
	}

	// Scope = repo A's type/interface IDs only (repo B did not change). The
	// parent Base lives in B and is NOT in scope; the scoped pass must still
	// re-derive Derived.Do -> Base.Do because the child Derived is in scope.
	scoped := crossRepoOverrideFixture()
	New(scoped).InferOverridesScoped(map[string]bool{"A::a.go::Derived": true})
	got := overrideEdgeSet(scoped)
	if len(got) != len(want) {
		t.Fatalf("scoped cross-repo override = %v, want %v", got, want)
	}
	for k := range want {
		if !got[k] {
			t.Errorf("scoped run dropped cross-repo override %q", k)
		}
	}
}

func fnValueCandidate(from, filePath, name string) *graph.Edge {
	return &graph.Edge{
		From:     from,
		To:       "unresolved::fnvalue::" + name,
		Kind:     graph.EdgeReferences,
		FilePath: filePath,
		Meta: map[string]any{
			"via":              fnValueCandidateVia,
			metaFnValueName:    name,
			"fn_value_ungated": true,
		},
	}
}

func callbackEdgeSet(g graph.Store) map[string]bool {
	out := map[string]bool{}
	for _, e := range g.AllEdges() {
		if e.Kind != graph.EdgeReferences || e.Meta == nil {
			continue
		}
		if via, _ := e.Meta["via"].(string); via == fnValueRegistrationVia {
			out[e.From+"->"+e.To] = true
		}
	}
	return out
}

// crossRepoFnValueFixture builds a callback registration in repo A whose handler
// is a uniquely-named function in the (unchanged) repo B, plus a second
// registration wholly inside repo B. The scoped run over {A} must still bind
// A's callback to the B handler (resolution is whole-graph) while never touching
// B's own candidate.
func crossRepoFnValueFixture() *graph.Graph {
	g := graph.New()
	// Repo B: two handler functions plus a B-local callback registration.
	g.AddNode(&graph.Node{ID: "B::b.go::HandlerB", Kind: graph.KindFunction, Name: "HandlerB", RepoPrefix: "B", FilePath: "b.go"})
	g.AddNode(&graph.Node{ID: "B::b.go::HandlerB2", Kind: graph.KindFunction, Name: "HandlerB2", RepoPrefix: "B", FilePath: "b.go"})
	g.AddNode(&graph.Node{ID: "B::b.go::RegisterB", Kind: graph.KindFunction, Name: "RegisterB", RepoPrefix: "B", FilePath: "b.go"})
	g.AddEdge(fnValueCandidate("B::b.go::RegisterB", "b.go", "HandlerB2"))
	// Repo A: a registration referencing the cross-repo handler by name.
	g.AddNode(&graph.Node{ID: "A::a.go::RegisterA", Kind: graph.KindFunction, Name: "RegisterA", RepoPrefix: "A", FilePath: "a.go"})
	g.AddEdge(fnValueCandidate("A::a.go::RegisterA", "a.go", "HandlerB"))
	return g
}

// TestResolveFnValueCallbacksScoped_CrossRepoHandler asserts the scoped fn-value
// gate binds a changed repo's callback to a handler in an UNCHANGED repo (the
// resolution side stays whole-graph), drives its candidate scan off GetRepoEdges
// rather than the whole-graph EdgeReferences scan, and materialises fewer nodes
// than the unscoped run (it never resolves the unchanged repo's own candidate).
func TestResolveFnValueCallbacksScoped_CrossRepoHandler(t *testing.T) {
	// Unscoped: both candidates bind.
	full := newCountingStore(crossRepoFnValueFixture())
	if n := ResolveFnValueCallbacks(full); n != 2 {
		t.Fatalf("unscoped fn-value: expected 2 bound callbacks, got %d", n)
	}
	fullEdges := callbackEdgeSet(full)
	if !fullEdges["A::a.go::RegisterA->B::b.go::HandlerB"] {
		t.Fatalf("unscoped did not bind the cross-repo callback: %v", fullEdges)
	}
	if full.edgesByKind == 0 {
		t.Errorf("unscoped fn-value should scan EdgesByKind(references) whole-graph")
	}

	// Scoped over {A}: only A's candidate is gated, but it still binds to the B
	// handler via the whole-graph name resolution.
	scoped := newCountingStore(crossRepoFnValueFixture())
	if n := ResolveFnValueCallbacksScoped(scoped, map[string]bool{"A": true}); n != 1 {
		t.Fatalf("scoped fn-value over {A}: expected 1 bound callback, got %d", n)
	}
	scopedEdges := callbackEdgeSet(scoped)
	if !scopedEdges["A::a.go::RegisterA->B::b.go::HandlerB"] {
		t.Errorf("scoped run dropped the cross-repo callback binding: %v", scopedEdges)
	}
	if scopedEdges["B::b.go::RegisterB->B::b.go::HandlerB2"] {
		t.Errorf("scoped run must NOT re-bind the unchanged repo's own candidate: %v", scopedEdges)
	}

	// Candidate scan path: scoped drives off GetRepoEdges(A), never the
	// whole-graph EdgeReferences scan.
	if scoped.edgesByKind != 0 {
		t.Errorf("scoped fn-value must not scan EdgesByKind whole-graph, got %d calls", scoped.edgesByKind)
	}
	if scoped.repoEdges["A"] == 0 {
		t.Errorf("scoped fn-value must scan GetRepoEdges(\"A\")")
	}
	// Fewer node reads: scoped skips resolving B's candidate entirely.
	if scoped.nodesReturned >= full.nodesReturned {
		t.Errorf("scoped run should materialise fewer nodes than unscoped: scoped=%d full=%d",
			scoped.nodesReturned, full.nodesReturned)
	}
}

func mkValueRefCandidate(from, filePath, name string) *graph.Edge {
	return &graph.Edge{
		From:     from,
		To:       "unresolved::value::" + name,
		Kind:     graph.EdgeReads,
		FilePath: filePath,
		Meta:     map[string]any{"via": valueRefCandidateVia, "name": name},
	}
}

// boundValueRefs maps reader -> constant for every value-ref read the pass bound
// (via == value_ref); a still-unbound candidate keeps the value_ref_candidate
// marker and is absent from the map.
func boundValueRefs(g graph.Store) map[string]string {
	out := map[string]string{}
	for _, e := range g.AllEdges() {
		if e.Kind != graph.EdgeReads || e.Meta == nil {
			continue
		}
		if via, _ := e.Meta["via"].(string); via == valueRefVia {
			out[e.From] = e.To
		}
	}
	return out
}

func crossRepoValueRefFixture() *graph.Graph {
	g := graph.New()
	// Repo A (changed): distinctive constant + a same-file reader candidate.
	g.AddNode(&graph.Node{ID: "A::a.go::MAX_SIZE", Kind: graph.KindConstant, Name: "MAX_SIZE", RepoPrefix: "A", FilePath: "a.go", StartLine: 1})
	g.AddNode(&graph.Node{ID: "A::a.go::useA", Kind: graph.KindFunction, Name: "useA", RepoPrefix: "A", FilePath: "a.go", StartLine: 5})
	g.AddEdge(mkValueRefCandidate("A::a.go::useA", "a.go", "MAX_SIZE"))
	// Repo B (unchanged): its own distinctive constant + same-file reader.
	g.AddNode(&graph.Node{ID: "B::b.go::MIN_SIZE", Kind: graph.KindConstant, Name: "MIN_SIZE", RepoPrefix: "B", FilePath: "b.go", StartLine: 1})
	g.AddNode(&graph.Node{ID: "B::b.go::useB", Kind: graph.KindFunction, Name: "useB", RepoPrefix: "B", FilePath: "b.go", StartLine: 5})
	g.AddEdge(mkValueRefCandidate("B::b.go::useB", "b.go", "MIN_SIZE"))
	return g
}

// TestResolveValueRefsScoped_CrossRepo asserts the scoped value-ref pass binds
// only the changed repo's candidate (leaving the unchanged repo's on-disk
// binding untouched), and drives its candidate scan off GetRepoEdges rather than
// the whole-graph EdgeReads scan. The unscoped pass binds both, proving the
// scoped pass is a strict narrowing of the same resolution.
func TestResolveValueRefsScoped_CrossRepo(t *testing.T) {
	full := crossRepoValueRefFixture()
	if n := ResolveValueRefs(full); n != 2 {
		t.Fatalf("unscoped value-ref: expected 2 bound reads, got %d", n)
	}
	fb := boundValueRefs(full)
	if fb["A::a.go::useA"] != "A::a.go::MAX_SIZE" || fb["B::b.go::useB"] != "B::b.go::MIN_SIZE" {
		t.Fatalf("unscoped value-ref bindings wrong: %v", fb)
	}

	scoped := newCountingStore(crossRepoValueRefFixture())
	if n := ResolveValueRefsScoped(scoped, map[string]bool{"A": true}); n != 1 {
		t.Fatalf("scoped value-ref over {A}: expected 1 bound read, got %d", n)
	}
	sb := boundValueRefs(scoped)
	if sb["A::a.go::useA"] != "A::a.go::MAX_SIZE" {
		t.Errorf("scoped run dropped repo A's value-ref binding: %v", sb)
	}
	if _, ok := sb["B::b.go::useB"]; ok {
		t.Errorf("scoped run must not bind the unchanged repo B's value-ref: %v", sb)
	}
	if scoped.edgesByKind != 0 {
		t.Errorf("scoped value-ref must not scan EdgesByKind whole-graph, got %d calls", scoped.edgesByKind)
	}
	if scoped.repoEdges["A"] == 0 {
		t.Errorf("scoped value-ref must scan GetRepoEdges(\"A\")")
	}
}
