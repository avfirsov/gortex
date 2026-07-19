package store_sqlite

import (
	"path/filepath"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestSQLiteEdgeMutationRevisionCoversSameKeyReplacementAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "edge-revision.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	edge := &graph.Edge{
		From: "repo::caller", To: graph.UnresolvedMarker + "Target", Kind: graph.EdgeCalls,
		FilePath: "pkg/a.go", Line: 7, Confidence: 0.25,
		Meta: map[string]any{"version": "original"},
	}

	before := store.EdgeMutationRevision()
	store.AddEdge(edge)
	afterInsert := store.EdgeMutationRevision()
	if afterInsert != before+1 {
		t.Fatalf("insert revision=%d after %d, want exactly one advance", afterInsert, before)
	}

	replacement := *edge
	replacement.Confidence = 0.95
	replacement.Meta = map[string]any{"version": "replacement"}
	store.AddEdge(&replacement)
	afterReplacement := store.EdgeMutationRevision()
	if afterReplacement != afterInsert {
		t.Fatalf("same-key INSERT OR IGNORE revision=%d after %d, want no-op", afterReplacement, afterInsert)
	}

	store.AddBatch(nil, []*graph.Edge{&replacement})
	afterBatchReplacement := store.EdgeMutationRevision()
	if afterBatchReplacement != afterReplacement {
		t.Fatalf("batch same-key INSERT OR IGNORE revision=%d after %d, want no-op", afterBatchReplacement, afterReplacement)
	}

	if !store.SetEdgeProvenance(&replacement, graph.OriginLSPResolved) {
		t.Fatal("set provenance")
	}
	afterProvenance := store.EdgeMutationRevision()
	if afterProvenance <= afterBatchReplacement {
		t.Fatalf("provenance revision=%d after %d, want advance", afterProvenance, afterBatchReplacement)
	}

	replacement.Confidence = 0.75
	replacement.Meta = map[string]any{"version": "attributes"}
	store.PersistEdgeAttributes(&replacement)
	afterAttributes := store.EdgeMutationRevision()
	if afterAttributes <= afterProvenance {
		t.Fatalf("attribute revision=%d after %d, want advance", afterAttributes, afterProvenance)
	}

	oldTo := replacement.To
	replacement.To = "repo::Target"
	store.ReindexEdges([]graph.EdgeReindex{{Edge: &replacement, OldTo: oldTo, OldKind: replacement.Kind}})
	afterReindex := store.EdgeMutationRevision()
	if afterReindex <= afterAttributes {
		t.Fatalf("reindex revision=%d after %d, want advance", afterReindex, afterAttributes)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	reopenedBefore := store.EdgeMutationRevision()
	if !store.RemoveEdge(replacement.From, replacement.To, replacement.Kind) {
		t.Fatal("remove replacement after reopen")
	}
	if afterRemove := store.EdgeMutationRevision(); afterRemove <= reopenedBefore {
		t.Fatalf("post-reopen remove revision=%d after %d, want advance", afterRemove, reopenedBefore)
	}
}
