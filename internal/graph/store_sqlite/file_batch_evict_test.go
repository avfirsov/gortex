package store_sqlite

import (
	"path/filepath"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestEvictFilesRemovesOnlyRequestedFilesInOneMutation(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "batch-evict.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	store.AddBatch([]*graph.Node{
		{ID: "a1", Kind: graph.KindFunction, Name: "a1", FilePath: "a.go"},
		{ID: "a2", Kind: graph.KindFunction, Name: "a2", FilePath: "a.go"},
		{ID: "b", Kind: graph.KindFunction, Name: "b", FilePath: "b.go"},
		{ID: "c", Kind: graph.KindFunction, Name: "c", FilePath: "c.go"},
	}, []*graph.Edge{
		{From: "a1", To: "a2", Kind: graph.EdgeCalls},
		{From: "a1", To: "b", Kind: graph.EdgeCalls},
		{From: "b", To: "a2", Kind: graph.EdgeReferences},
		{From: "b", To: "c", Kind: graph.EdgeCalls},
		{From: "c", To: "b", Kind: graph.EdgeCalls},
	})

	nodes, edges := store.EvictFiles([]string{"", "a.go", "a.go"})
	if nodes != 2 || edges != 3 {
		t.Fatalf("EvictFiles removed nodes=%d edges=%d, want 2/3", nodes, edges)
	}
	if got := store.GetFileNodes("a.go"); len(got) != 0 {
		t.Fatalf("evicted file retained %d nodes", len(got))
	}
	if store.GetNode("b") == nil || store.GetNode("c") == nil {
		t.Fatal("unrequested file nodes were removed")
	}
	if got := store.EdgeCount(); got != 2 {
		t.Fatalf("surviving edge count=%d, want 2", got)
	}
	if got := len(store.GetOutEdges("b")); got != 1 || store.GetOutEdges("b")[0].To != "c" {
		t.Fatalf("b adjacency after batch eviction = %#v", store.GetOutEdges("b"))
	}
}

func TestBatchDeleteSymbolFTSUsesOwnershipSidecarAndPreservesSiblings(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "batch-fts-delete.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	store.AddBatch([]*graph.Node{
		{ID: "a", Kind: graph.KindFunction, Name: "a", FilePath: "a.go", RepoPrefix: "repo"},
		{ID: "b", Kind: graph.KindFunction, Name: "b", FilePath: "b.go", RepoPrefix: "repo"},
	}, nil)
	if err := store.BatchUpsertSymbolFTS([]graph.SymbolFTSItem{
		{NodeID: "a", Tokens: "zqxalpha"},
		{NodeID: "b", Tokens: "zqxbeta"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.BatchDeleteSymbolFTS([]string{"", "a", "a", "missing"}); err != nil {
		t.Fatal(err)
	}

	alpha, err := store.SearchSymbols("zqxalpha", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(alpha) != 0 {
		t.Fatalf("deleted symbol remained searchable: %#v", alpha)
	}
	beta, err := store.SearchSymbols("zqxbeta", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(beta) != 1 || beta[0].NodeID != "b" {
		t.Fatalf("sibling symbol search = %#v, want b", beta)
	}
	var ownership int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM symbol_fts_rowid WHERE node_id = 'a'`).Scan(&ownership); err != nil {
		t.Fatal(err)
	}
	if ownership != 0 {
		t.Fatalf("deleted symbol retained %d ownership rows", ownership)
	}
}
