package graph

import "testing"

func TestGraphMutationRevisionCoversNodeOnlyWrites(t *testing.T) {
	g := New()
	node := &Node{ID: "repo::target", Kind: KindFunction, Name: "Target", FilePath: "pkg/a.go", RepoPrefix: "repo"}

	edgeBefore := g.EdgeMutationRevision()
	before := g.MutationRevision()
	g.AddNode(node)
	afterAdd := g.MutationRevision()
	if afterAdd <= before {
		t.Fatalf("node add revision=%d after %d, want advance", afterAdd, before)
	}
	if got := g.EdgeMutationRevision(); got != edgeBefore {
		t.Fatalf("node add changed edge revision from %d to %d", edgeBefore, got)
	}

	replacement := *node
	replacement.Name = "TargetV2"
	g.AddBatch([]*Node{&replacement}, nil)
	afterReplacement := g.MutationRevision()
	if afterReplacement <= afterAdd {
		t.Fatalf("node replacement revision=%d after %d, want advance", afterReplacement, afterAdd)
	}
	if got := g.EdgeMutationRevision(); got != edgeBefore {
		t.Fatalf("node replacement changed edge revision from %d to %d", edgeBefore, got)
	}

	nodes, edges := g.EvictFile(replacement.FilePath)
	if nodes != 1 || edges != 0 {
		t.Fatalf("evict counts nodes=%d edges=%d, want 1/0", nodes, edges)
	}
	if afterEvict := g.MutationRevision(); afterEvict <= afterReplacement {
		t.Fatalf("node eviction revision=%d after %d, want advance", afterEvict, afterReplacement)
	}
	if got := g.EdgeMutationRevision(); got != edgeBefore {
		t.Fatalf("node eviction changed edge revision from %d to %d", edgeBefore, got)
	}
}
