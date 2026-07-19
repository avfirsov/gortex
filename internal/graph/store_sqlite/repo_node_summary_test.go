package store_sqlite

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestRepoNodeSummariesByLanguageAreExactAndMetadataFree(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "graph.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	store.AddBatch([]*graph.Node{
		{ID: "a/main.go::GoType", Name: "GoType", Kind: graph.KindType, FilePath: "a/main.go", Language: "go", RepoPrefix: "a", StartLine: 3, EndLine: 5, Meta: map[string]any{"doc": "large promoted doc", "signature": "type GoType struct{}", "custom": "opaque"}},
		{ID: "a/main.py::PyType", Name: "PyType", Kind: graph.KindType, FilePath: "a/main.py", Language: "python", RepoPrefix: "a", StartLine: 7, EndLine: 9},
		{ID: "b/main.go::Other", Name: "Other", Kind: graph.KindType, FilePath: "b/main.go", Language: "go", RepoPrefix: "b", StartLine: 1, EndLine: 2},
		{ID: "main.go::Solo", Name: "Solo", Kind: graph.KindType, FilePath: "main.go", Language: "go", StartLine: 11, EndLine: 12, Meta: map[string]any{"doc": "must not cross"}},
	}, nil)

	aGo := store.GetRepoNodeSummariesByLanguage("a", "go")
	require.Len(t, aGo, 1)
	assert.Equal(t, "a/main.go::GoType", aGo[0].ID)
	assert.Equal(t, graph.KindType, aGo[0].Kind)
	assert.Equal(t, 3, aGo[0].StartLine)
	assert.Nil(t, aGo[0].Meta, "summary projection must not restore promoted or opaque metadata")

	emptyGo := store.GetRepoNodeSummariesByLanguage("", "go")
	require.Len(t, emptyGo, 1, "empty prefix must match only the unscoped repository")
	assert.Equal(t, "main.go::Solo", emptyGo[0].ID)
	assert.Nil(t, emptyGo[0].Meta)
	assert.Empty(t, store.GetRepoNodeSummariesByLanguage("a", ""))
}
