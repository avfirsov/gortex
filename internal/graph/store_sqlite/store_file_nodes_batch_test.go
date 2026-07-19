package store_sqlite

import (
	"path/filepath"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestGetFileNodesByPathsGroupsAndDedupes(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "file-nodes.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	store.AddBatch([]*graph.Node{
		{ID: "a.go", Kind: graph.KindFile, Name: "a.go", FilePath: "a.go"},
		{ID: "a.go::A", Kind: graph.KindFunction, Name: "A", FilePath: "a.go"},
		{ID: "b.go::B", Kind: graph.KindFunction, Name: "B", FilePath: "b.go"},
		{ID: "c.go::C", Kind: graph.KindFunction, Name: "C", FilePath: "c.go"},
	}, nil)

	got := store.GetFileNodesByPaths([]string{"a.go", "b.go", "a.go", "", "missing.go"})
	if len(got["a.go"]) != 2 || len(got["b.go"]) != 1 {
		t.Fatalf("group sizes = a:%d b:%d, want 2/1", len(got["a.go"]), len(got["b.go"]))
	}
	if _, present := got["c.go"]; present {
		t.Fatal("unrequested c.go bucket returned")
	}
	if _, present := got["missing.go"]; present {
		t.Fatal("missing path must be absent")
	}
}
