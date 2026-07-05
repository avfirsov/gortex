package store_sqlite

import (
	"path/filepath"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// TestGetRepoNodesLight verifies the graph.LightNodeReader fast path
// matches GetRepoNodes on IDs and promoted-field values, stays scoped to
// repo_prefix, and never surfaces non-promoted meta content — the
// invariant the enrichment hover-candidate refetch depends on for
// correctness (see EnrichRepoContext's use of repoScopedNodesLight).
func TestGetRepoNodesLight(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "light.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.AddNode(&graph.Node{
		ID: "repoA/f.go::Stamped", Kind: graph.KindFunction, Name: "Stamped",
		FilePath: "repoA/f.go", RepoPrefix: "repoA",
		Meta: map[string]any{
			"semantic_type":   "string",
			"semantic_source": "lsp-gopls",
			"doc":             "docs",
			"complexity":      7, // non-promoted
		},
	})
	s.AddNode(&graph.Node{
		ID: "repoA/f.go::Unstamped", Kind: graph.KindFunction, Name: "Unstamped",
		FilePath: "repoA/f.go", RepoPrefix: "repoA",
	})
	s.AddNode(&graph.Node{
		ID: "repoB/g.go::Other", Kind: graph.KindFunction, Name: "Other",
		FilePath: "repoB/g.go", RepoPrefix: "repoB",
	})

	var _ graph.LightNodeReader = s // compile-time capability check

	full := s.GetRepoNodes("repoA")
	light := s.GetRepoNodesLight("repoA")
	if len(light) != len(full) {
		t.Fatalf("light returned %d nodes, full returned %d", len(light), len(full))
	}

	byID := make(map[string]*graph.Node, len(light))
	for _, n := range light {
		byID[n.ID] = n
	}

	stamped, ok := byID["repoA/f.go::Stamped"]
	if !ok {
		t.Fatal("light scan missing the stamped node")
	}
	assertType[string](t, stamped.Meta, "semantic_type", "string")
	assertType[string](t, stamped.Meta, "semantic_source", "lsp-gopls")
	assertType[string](t, stamped.Meta, "doc", "docs")
	if _, ok := stamped.Meta["complexity"]; ok {
		t.Errorf("light scan must not surface non-promoted meta, got complexity=%v", stamped.Meta["complexity"])
	}

	unstamped, ok := byID["repoA/f.go::Unstamped"]
	if !ok {
		t.Fatal("light scan missing the unstamped node")
	}
	if _, ok := unstamped.Meta["semantic_type"]; ok {
		t.Error("unstamped node must not carry a semantic_type key")
	}

	if _, ok := byID["repoB/g.go::Other"]; ok {
		t.Error("light scan crossed repo_prefix scope")
	}
}
