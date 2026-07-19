package store_sqlite

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestBackfillWorkspaceSlugsIsDurableAndIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.sqlite")
	store, err := Open(path)
	require.NoError(t, err)
	store.AddBatch([]*graph.Node{
		{ID: "repoA/a.go::A", Kind: graph.KindFunction, RepoPrefix: "repoA", Meta: map[string]any{"opaque": "keep"}},
		{ID: "repoA/b.go::B", Kind: graph.KindFunction, RepoPrefix: "repoA"},
		{ID: "repoB/c.go::C", Kind: graph.KindFunction, RepoPrefix: "repoB", WorkspaceID: "existing"},
	}, nil)

	rows := []graph.WorkspaceSlug{
		{RepoPrefix: "repoA", Workspace: "workspace-a", Project: "project-a"},
		{RepoPrefix: "repoB", Workspace: "workspace-b", Project: "project-b"},
	}
	beforeRevision := store.AnalysisMutationRevision()
	require.Equal(t, 3, store.BackfillWorkspaceSlugs(rows))
	require.Equal(t, beforeRevision+1, store.AnalysisMutationRevision())

	a := store.GetNode("repoA/a.go::A")
	require.NotNil(t, a)
	require.Equal(t, "workspace-a", a.WorkspaceID)
	require.Equal(t, "project-a", a.ProjectID)
	require.Equal(t, "keep", a.Meta["opaque"])
	c := store.GetNode("repoB/c.go::C")
	require.NotNil(t, c)
	require.Equal(t, "existing", c.WorkspaceID, "non-empty workspace must be preserved")
	require.Equal(t, "project-b", c.ProjectID)

	beforeRevision = store.AnalysisMutationRevision()
	require.Zero(t, store.BackfillWorkspaceSlugs(rows))
	require.Equal(t, beforeRevision, store.AnalysisMutationRevision())
	require.NoError(t, store.Close())

	store, err = Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	a = store.GetNode("repoA/a.go::A")
	require.NotNil(t, a)
	require.Equal(t, "workspace-a", a.WorkspaceID)
	require.Equal(t, "project-a", a.ProjectID)
	require.Equal(t, "keep", a.Meta["opaque"])
}
