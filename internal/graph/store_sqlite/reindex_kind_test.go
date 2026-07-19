package store_sqlite

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestSQLiteReindexEdgesKindChangeRemovesOldIdentity(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "graph.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	edge := &graph.Edge{
		From: "repo/caller.go::Caller", To: graph.UnresolvedMarker + "Target",
		Kind: graph.EdgeReads, FilePath: "repo/caller.go", Line: 17,
		Meta: map[string]any{"receiver_type": "Service"},
	}
	store.AddBatch([]*graph.Node{{
		ID: edge.From, Kind: graph.KindFunction, Name: "Caller",
		FilePath: edge.FilePath, RepoPrefix: "repo",
	}}, []*graph.Edge{edge})

	oldTo := edge.To
	oldKind := edge.Kind
	edge.To = "repo/target.go::Target"
	edge.Kind = graph.EdgeReferences
	edge.Origin = graph.OriginASTResolved
	store.ReindexEdges([]graph.EdgeReindex{{
		Edge: edge, OldTo: oldTo, OldKind: oldKind,
	}})

	out := store.GetOutEdges(edge.From)
	require.Len(t, out, 1)
	require.Equal(t, graph.EdgeReferences, out[0].Kind)
	require.Equal(t, edge.To, out[0].To)
	require.Equal(t, graph.OriginASTResolved, out[0].Origin)

	var oldRows, newRows, sourceRows int
	require.NoError(t, store.db.QueryRow(`
SELECT COUNT(*) FROM edges
WHERE from_id = ? AND to_id = ? AND kind = ? AND file_path = ? AND line = ?`,
		edge.From, oldTo, string(oldKind), edge.FilePath, edge.Line,
	).Scan(&oldRows))
	require.NoError(t, store.db.QueryRow(`
SELECT COUNT(*) FROM edges
WHERE from_id = ? AND to_id = ? AND kind = ? AND file_path = ? AND line = ?`,
		edge.From, edge.To, string(edge.Kind), edge.FilePath, edge.Line,
	).Scan(&newRows))
	require.NoError(t, store.db.QueryRow(`SELECT COUNT(*) FROM edges WHERE from_id = ?`, edge.From).Scan(&sourceRows))
	require.Zero(t, oldRows, "old unresolved Reads identity must be deleted")
	require.Equal(t, 1, newRows, "new References identity must exist exactly once")
	require.Equal(t, 1, sourceRows, "kind change must not leave a duplicate source row")
}
