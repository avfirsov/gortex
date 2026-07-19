package store_sqlite_test

import (
	"path/filepath"
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_sqlite"
)

func TestLSPProjectionPushesScopeAndStampPredicatesIntoSQLite(t *testing.T) {
	store, err := store_sqlite.Open(filepath.Join(t.TempDir(), "lsp-projection.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	nodes := []*graph.Node{
		{ID: "a", RepoPrefix: "repo", Language: "go", Kind: graph.KindFunction, Name: "A", FilePath: "repo/a.go", StartLine: 1, EndLine: 5, Meta: map[string]any{"opaque": "must-not-cross-light-scan"}},
		{ID: "b", RepoPrefix: "repo", Language: "go", Kind: graph.KindMethod, Name: "B", FilePath: "repo/b.go", StartLine: 2, EndLine: 6, Meta: map[string]any{"semantic_type": "func()", "opaque": "must-not-cross-light-scan"}},
		{ID: "file", RepoPrefix: "repo", Language: "go", Kind: graph.KindFile, Name: "a.go", FilePath: "repo/a.go"},
		{ID: "py", RepoPrefix: "repo", Language: "python", Kind: graph.KindFunction, Name: "Py", FilePath: "repo/py.py", StartLine: 1, EndLine: 2},
		{ID: "other", RepoPrefix: "other", Language: "go", Kind: graph.KindFunction, Name: "Other", FilePath: "other/a.go", StartLine: 1, EndLine: 2},
	}
	edges := []*graph.Edge{
		{From: "a", To: "b", Kind: graph.EdgeCalls, FilePath: "repo/a.go", Line: 3, Confidence: 0.5},
		{From: "a", To: "b", Kind: graph.EdgeReferences, FilePath: "repo/a.go", Line: 4, Confidence: 1},
		{From: "a", To: "b", Kind: graph.EdgeMemberOf, FilePath: "repo/a.go", Line: 1, Confidence: 0.5},
		{From: "a", To: "b", Kind: graph.EdgeDefines, FilePath: "repo/a.go", Line: 1, Confidence: 0.5},
		{From: "py", To: "a", Kind: graph.EdgeCalls, FilePath: "repo/py.py", Line: 1, Confidence: 0.5},
		{From: "other", To: "a", Kind: graph.EdgeCalls, FilePath: "other/a.go", Line: 1, Confidence: 0.5},
	}
	store.AddBatch(nodes, edges)

	totals, unstamped := store.LSPRepoFileCounts("repo", []string{"go"})
	if totals["repo/a.go"] != 1 || totals["repo/b.go"] != 1 || len(totals) != 2 {
		t.Fatalf("symbol totals = %v, want one Go symbol in a.go and b.go", totals)
	}
	if unstamped["repo/a.go"] != 1 || unstamped["repo/b.go"] != 0 {
		t.Fatalf("unstamped totals = %v, want only a.go pending", unstamped)
	}

	files := []string{"repo/a.go", "repo/b.go", "repo/py.py", "other/a.go"}
	projected := store.LSPRepoNodesByFiles("repo", []string{"go"}, files, false)
	if len(projected) != 2 {
		t.Fatalf("projected nodes = %d, want a and b only", len(projected))
	}
	byID := make(map[string]*graph.Node, len(projected))
	for _, node := range projected {
		byID[node.ID] = node
	}
	if byID["a"] == nil || byID["b"] == nil {
		t.Fatalf("projected node IDs = %v, want a and b", byID)
	}
	if _, leaked := byID["a"].Meta["opaque"]; leaked {
		t.Fatal("opaque Meta crossed the light projection")
	}
	if got, _ := byID["b"].Meta["semantic_type"].(string); got != "func()" {
		t.Fatalf("promoted semantic_type = %q, want func()", got)
	}
	pending := store.LSPRepoNodesByFiles("repo", []string{"go"}, files, true)
	if len(pending) != 1 || pending[0].ID != "a" {
		t.Fatalf("unstamped projection = %v, want only a", nodeIDs(pending))
	}

	confirmable := store.LSPRepoConfirmableEdgesByFiles("repo", []string{"go"}, files, false)
	if len(confirmable) != 2 || confirmable[0].Kind != graph.EdgeCalls || confirmable[1].Kind != graph.EdgeReferences {
		t.Fatalf("confirmable kinds = %v, want calls and references", edgeKinds(confirmable))
	}
	ambiguous := store.LSPRepoConfirmableEdgesByFiles("repo", []string{"go"}, files, true)
	if len(ambiguous) != 1 || ambiguous[0].Kind != graph.EdgeCalls {
		t.Fatalf("ambiguous confirmable kinds = %v, want calls only", edgeKinds(ambiguous))
	}
	memberOf := store.LSPRepoEdgesByFilesAndKinds("repo", []string{"go"}, files, []graph.EdgeKind{graph.EdgeMemberOf})
	if len(memberOf) != 1 || memberOf[0].Kind != graph.EdgeMemberOf {
		t.Fatalf("member_of projection = %v, want one member_of", edgeKinds(memberOf))
	}
}

func nodeIDs(nodes []*graph.Node) []string {
	ids := make([]string, 0, len(nodes))
	for _, node := range nodes {
		ids = append(ids, node.ID)
	}
	return ids
}

func edgeKinds(edges []*graph.Edge) []graph.EdgeKind {
	kinds := make([]graph.EdgeKind, 0, len(edges))
	for _, edge := range edges {
		kinds = append(kinds, edge.Kind)
	}
	return kinds
}
