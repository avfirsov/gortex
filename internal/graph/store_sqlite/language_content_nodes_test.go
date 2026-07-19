package store_sqlite

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestLanguageAndContentNodeProjections(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "graph.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	store.AddBatch([]*graph.Node{
		{ID: "repoA/a.go::A", Kind: graph.KindFunction, Language: "go", RepoPrefix: "repoA"},
		{ID: "repoB/b.go::B", Kind: graph.KindFunction, Language: "go", RepoPrefix: "repoB"},
		{ID: "repoA/c.py::C", Kind: graph.KindFunction, Language: "python", RepoPrefix: "repoA"},
		{ID: "repoA/doc::1", Kind: graph.KindDoc, Language: "text", RepoPrefix: "repoA", Meta: map[string]any{"data_class": "content", "section_text": "body"}},
		{ID: "repoB/doc::1", Kind: graph.KindDoc, Language: "markdown", RepoPrefix: "repoB", Meta: map[string]any{"section_text": "ordinary prose"}},
	}, nil)

	goNodes := store.GetNodesByLanguage("go")
	require.Len(t, goNodes, 2)
	require.Equal(t, "repoA/a.go::A", goNodes[0].ID)
	require.Equal(t, "repoB/b.go::B", goNodes[1].ID)

	content := store.GetRepoContentNodes("repoA")
	require.Len(t, content, 1)
	require.Equal(t, "repoA/doc::1", content[0].ID)
	require.Equal(t, "body", content[0].Meta["section_text"])
	require.Empty(t, store.GetRepoContentNodes("repoB"), "ordinary prose is not CONTENT")
}
