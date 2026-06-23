package indexer

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// TestPersistFileMeta_PopulatesFilesSidecar pins the end-to-end population of
// the per-file metadata sidecar: indexing a file records a row with a non-empty
// content hash, a positive byte size, and the extracted node count — the data
// index_health reports per file.
func TestPersistFileMeta_PopulatesFilesSidecar(t *testing.T) {
	src := `package main

func Alpha() {}

func Beta() {}
`
	g := indexAll(t, src) // single-repo mode → repoPrefix ""

	reader, ok := g.(graph.FileMetaReader)
	if !ok {
		t.Fatal("in-memory graph must implement FileMetaReader")
	}
	rows, err := reader.FileMetasForRepo("")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("no file metadata rows recorded after indexing")
	}
	var found bool
	for _, r := range rows {
		if r.FilePath != "main.go" {
			continue
		}
		found = true
		if r.ContentHash == "" {
			t.Error("content_hash empty")
		}
		if r.Size == 0 {
			t.Errorf("size = 0, want > 0")
		}
		if r.NodeCount == 0 {
			t.Errorf("node_count = 0, want > 0 (file node + 2 functions)")
		}
		if r.Errors != "" {
			t.Errorf("clean file recorded errors: %q", r.Errors)
		}
	}
	if !found {
		t.Errorf("no row for main.go; rows = %+v", rows)
	}
}
