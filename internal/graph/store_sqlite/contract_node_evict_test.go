package store_sqlite

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestEvictContractNodesByIDsIsExactAndSetOriented(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "graph.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	store.AddBatch([]*graph.Node{
		{ID: "fn", Kind: graph.KindFunction, Name: "fn", FilePath: "a.go"},
		{ID: "keep", Kind: graph.KindContract, Name: "keep", FilePath: "a.go"},
		{ID: "drop-a", Kind: graph.KindContract, Name: "drop-a", FilePath: "a.go"},
		{ID: "drop-b", Kind: graph.KindContract, Name: "drop-b", FilePath: "b.go"},
	}, []*graph.Edge{
		{From: "fn", To: "drop-a", Kind: graph.EdgeProvides, FilePath: "a.go"},
		{From: "drop-b", To: "fn", Kind: graph.EdgeCalls, FilePath: "b.go"},
		{From: "fn", To: "keep", Kind: graph.EdgeProvides, FilePath: "a.go"},
	})

	nodes, edges := store.EvictContractNodesByIDs([]string{"drop-b", "drop-a", "drop-a", "fn"})
	assert.Equal(t, 2, nodes)
	assert.Equal(t, 2, edges)
	assert.Nil(t, store.GetNode("drop-a"))
	assert.Nil(t, store.GetNode("drop-b"))
	assert.NotNil(t, store.GetNode("fn"), "non-contract ID must be ignored")
	assert.NotNil(t, store.GetNode("keep"))
	require.Len(t, store.GetOutEdges("fn"), 1)
	assert.Equal(t, "keep", store.GetOutEdges("fn")[0].To)
}
