package store_sqlite

import (
	"path/filepath"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// TestFileMetas_RoundTrip pins the per-file metadata sidecar: rows upsert,
// read back per repo, carry their errors JSON, and a per-file delete removes
// just the named file.
func TestFileMetas_RoundTrip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "f.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	rows := []graph.FileMetaRow{
		{FilePath: "a/x.go", ContentHash: "h1", Size: 100, NodeCount: 7, Errors: ""},
		{FilePath: "a/broken.go", ContentHash: "h2", Size: 50, NodeCount: 1, Errors: `["3:5","4:1"]`},
	}
	if err := s.SetFileMetas("repoA", rows); err != nil {
		t.Fatal(err)
	}
	// A different repo's row must not bleed in.
	if err := s.SetFileMetas("repoB", []graph.FileMetaRow{{FilePath: "b/y.go", NodeCount: 2}}); err != nil {
		t.Fatal(err)
	}

	got, err := s.FileMetasForRepo("repoA")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("FileMetasForRepo(repoA) = %d rows, want 2", len(got))
	}
	byFile := map[string]graph.FileMetaRow{}
	for _, r := range got {
		byFile[r.FilePath] = r
	}
	if r := byFile["a/x.go"]; r.NodeCount != 7 || r.Size != 100 || r.ContentHash != "h1" || r.Errors != "" {
		t.Errorf("x.go row = %+v", r)
	}
	if r := byFile["a/broken.go"]; r.NodeCount != 1 || r.Errors != `["3:5","4:1"]` {
		t.Errorf("broken.go row = %+v", r)
	}

	// Upsert replaces in place.
	if err := s.SetFileMetas("repoA", []graph.FileMetaRow{{FilePath: "a/x.go", NodeCount: 9, Errors: ""}}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.FileMetasForRepo("repoA")
	for _, r := range got {
		if r.FilePath == "a/x.go" && r.NodeCount != 9 {
			t.Errorf("upsert did not replace node_count: %+v", r)
		}
	}

	// Per-file delete removes only the named file.
	if err := s.DeleteFileMetasByFiles("repoA", []string{"a/broken.go"}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.FileMetasForRepo("repoA")
	if len(got) != 1 || got[0].FilePath != "a/x.go" {
		t.Errorf("after delete, rows = %+v, want only a/x.go", got)
	}
}
