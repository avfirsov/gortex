package indexer

import (
	"context"
	"iter"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// idxCountingStore wraps a graph.Store and records which node-read paths a pass
// takes, so a test can prove a repo-scoped pass drives off GetRepoNodes /
// GetRepoEdges for the changed repo and never materialises another repo's nodes
// via a whole-graph AllNodes / NodesByKind scan.
type idxCountingStore struct {
	graph.Store
	allNodes      int
	repoNodes     map[string]int
	repoEdges     map[string]int
	nodesReturned int
}

func newIdxCountingStore(s graph.Store) *idxCountingStore {
	return &idxCountingStore{Store: s, repoNodes: map[string]int{}, repoEdges: map[string]int{}}
}

func (c *idxCountingStore) AllNodes() []*graph.Node {
	c.allNodes++
	ns := c.Store.AllNodes()
	c.nodesReturned += len(ns)
	return ns
}

func (c *idxCountingStore) GetRepoNodes(prefix string) []*graph.Node {
	c.repoNodes[prefix]++
	ns := c.Store.GetRepoNodes(prefix)
	c.nodesReturned += len(ns)
	return ns
}

func (c *idxCountingStore) GetRepoEdges(prefix string) []*graph.Edge {
	c.repoEdges[prefix]++
	return c.Store.GetRepoEdges(prefix)
}

func (c *idxCountingStore) GetFileNodes(path string) []*graph.Node {
	ns := c.Store.GetFileNodes(path)
	c.nodesReturned += len(ns)
	return ns
}

func (c *idxCountingStore) NodesByKind(k graph.NodeKind) iter.Seq[*graph.Node] {
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

// twoRepoFuncGraph builds a graph with a handful of function/method nodes in
// each of two repos, so the per-repo readers have something to return.
func twoRepoFuncGraph() *graph.Graph {
	g := graph.New()
	for _, spec := range []struct{ repo, file string }{{"repoA", "a.go"}, {"repoB", "b.go"}} {
		g.AddNode(&graph.Node{ID: spec.repo + "::" + spec.file + "::Fn", Kind: graph.KindFunction, Name: "Fn", RepoPrefix: spec.repo, FilePath: spec.file})
		g.AddNode(&graph.Node{ID: spec.repo + "::" + spec.file + "::Fn2", Kind: graph.KindFunction, Name: "Fn2", RepoPrefix: spec.repo, FilePath: spec.file})
	}
	return g
}

// TestCloneRepoNodes_ScopedNeverMaterialisesOtherRepo asserts the clone detect
// and incremental Rebuild passes for one repo read only that repo's nodes via
// GetRepoNodes — never the whole-graph AllNodes scan, and never the sibling
// repo's node bucket.
func TestCloneRepoNodes_ScopedNeverMaterialisesOtherRepo(t *testing.T) {
	cs := newIdxCountingStore(twoRepoFuncGraph())

	// Detect for repoA: finalise + detect both walk cloneRepoNodes(repoA).
	detectClonesAndEmitEdgesCtx(context.Background(), cs, "repoA", 0.8)
	// Incremental index rebuild for repoA reseeds from the same repo's nodes.
	ci := newIncrementalCloneIndex()
	ci.Rebuild(cs, "repoA")

	if cs.allNodes != 0 {
		t.Errorf("clone passes for repoA must not call AllNodes(); got %d calls", cs.allNodes)
	}
	if cs.repoNodes["repoA"] == 0 {
		t.Errorf("clone passes for repoA must read via GetRepoNodes(\"repoA\")")
	}
	if cs.repoNodes["repoB"] != 0 {
		t.Errorf("clone passes for repoA must never materialise repoB's nodes; got %d GetRepoNodes(\"repoB\") calls", cs.repoNodes["repoB"])
	}
}

// TestCloneRepoNodes_EmptyPrefixFallsBackToAllNodes asserts the single-repo /
// in-memory regime (empty prefix, nodes not tracked in the byRepo buckets) still
// uses the AllNodes fallback — GetRepoNodes("") would be empty.
func TestCloneRepoNodes_EmptyPrefixFallsBackToAllNodes(t *testing.T) {
	cs := newIdxCountingStore(twoRepoFuncGraph())
	detectClonesAndEmitEdgesCtx(context.Background(), cs, "", 0.8)
	if cs.allNodes == 0 {
		t.Errorf("empty-prefix clone detect must fall back to AllNodes()")
	}
}

func accessesFieldFrom(g graph.Store, repoPrefix string) map[string]bool {
	out := map[string]bool{}
	for _, e := range g.AllEdges() {
		if e.Kind != graph.EdgeAccessesField {
			continue
		}
		n := g.GetNode(e.From)
		if n != nil && n.RepoPrefix == repoPrefix {
			out[e.From+"->"+e.To] = true
		}
	}
	return out
}

// capabilityFixture builds a field write in repo A (the changed repo) plus a
// large field population in repo B, so the whole-graph fieldIDs scan the
// unscoped capability pass runs materialises far more nodes than the scoped pass
// that reads only repo A's nodes.
func capabilityFixture() *graph.Graph {
	g := graph.New()
	g.AddNode(&graph.Node{ID: "repoA::a.go::Foo", Kind: graph.KindType, Name: "Foo", RepoPrefix: "repoA", FilePath: "a.go"})
	g.AddNode(&graph.Node{ID: "repoA::a.go::Foo.set", Kind: graph.KindMethod, Name: "set", RepoPrefix: "repoA", FilePath: "a.go", Meta: map[string]any{"receiver": "Foo"}})
	g.AddNode(&graph.Node{ID: "repoA::a.go::Foo.count", Kind: graph.KindField, Name: "count", RepoPrefix: "repoA", FilePath: "a.go", Meta: map[string]any{"receiver": "Foo"}})
	g.AddEdge(&graph.Edge{From: "repoA::a.go::Foo.set", To: "repoA::a.go::Foo.count", Kind: graph.EdgeWrites, FilePath: "a.go"})
	// Repo B: a large, unchanged field population the scoped pass must not scan.
	for i := 0; i < 80; i++ {
		id := "repoB::b.go::F" + string(rune('A'+i%26)) + string(rune('0'+i/26))
		g.AddNode(&graph.Node{ID: id, Kind: graph.KindField, Name: "f", RepoPrefix: "repoB", FilePath: "b.go", Meta: map[string]any{"receiver": "Bar"}})
	}
	return g
}

// TestSynthesizeCapabilityEdgesScoped_ParityAndFewerReads asserts the scoped
// capability pass emits the same accesses_field edge for the changed repo as the
// unscoped pass, drives its sweep off GetRepoEdges, and materialises fewer nodes
// (it never runs the whole-graph KindField scan that seeds fieldIDs).
func TestSynthesizeCapabilityEdgesScoped_ParityAndFewerReads(t *testing.T) {
	full := newIdxCountingStore(capabilityFixture())
	synthesizeCapabilityEdges(full)
	wantA := accessesFieldFrom(full, "repoA")
	if !wantA["repoA::a.go::Foo.set->repoA::a.go::Foo.count"] {
		t.Fatalf("unscoped pass did not emit repo A's accesses_field edge: %v", wantA)
	}

	scoped := newIdxCountingStore(capabilityFixture())
	synthesizeCapabilityEdgesScoped(scoped, map[string]bool{"repoA": true})
	gotA := accessesFieldFrom(scoped, "repoA")
	if len(gotA) != len(wantA) {
		t.Fatalf("scoped capability repo-A edges = %v, want %v", gotA, wantA)
	}
	for k := range wantA {
		if !gotA[k] {
			t.Errorf("scoped run dropped repo A's capability edge %q", k)
		}
	}

	if scoped.repoEdges["repoA"] == 0 {
		t.Errorf("scoped capability must drive its sweep off GetRepoEdges(\"repoA\")")
	}
	if scoped.nodesReturned >= full.nodesReturned {
		t.Errorf("scoped capability should materialise fewer nodes than unscoped: scoped=%d full=%d",
			scoped.nodesReturned, full.nodesReturned)
	}
}
