package store_sqlite_test

import (
	"path/filepath"
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_sqlite"
	"github.com/zzet/gortex/internal/graph/storetest"
)

func TestSQLiteStoreConformance(t *testing.T) {
	storetest.RunConformance(t, func(t *testing.T) graph.Store {
		dir := t.TempDir()
		s, err := store_sqlite.Open(filepath.Join(dir, "test.sqlite"))
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	})
}

func TestAddNodeUpdatePreservesIncidentEdges(t *testing.T) {
	s, err := store_sqlite.Open(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	a := &graph.Node{ID: "a", Kind: graph.KindFunction, Name: "a"}
	b := &graph.Node{ID: "b", Kind: graph.KindFunction, Name: "b"}
	s.AddBatch([]*graph.Node{a, b}, []*graph.Edge{{From: "a", To: "b", Kind: graph.EdgeCalls}})
	if got := s.EdgeCount(); got != 1 {
		t.Fatalf("edge count before metadata update = %d, want 1", got)
	}

	b.Meta = map[string]any{"reach_build": uint64(7)}
	s.AddNode(b)
	if got := s.EdgeCount(); got != 1 {
		t.Fatalf("edge count after node upsert = %d, want 1", got)
	}
	if got := len(s.GetInEdges("b")); got != 1 {
		t.Fatalf("incoming edges after node upsert = %d, want 1", got)
	}
	if got := len(s.GetOutEdges("a")); got != 1 {
		t.Fatalf("outgoing edges after node upsert = %d, want 1", got)
	}
}
