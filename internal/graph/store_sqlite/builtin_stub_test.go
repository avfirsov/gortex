package store_sqlite

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

// The SQLite funnel mirror of the in-memory materialization test: an edge
// targeting a builtin stub must leave a real KindBuiltin node behind it (no
// dangling calls), and a warm re-index of the same target must ride the
// seen-set instead of re-upserting the stub.
func TestSQLiteAddBatchMaterializesBuiltinStubs(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "builtin.sqlite"))
	require.NoError(t, err)
	defer s.Close()

	caller := &graph.Node{ID: "web/src/a.ts::fn", Kind: graph.KindFunction, Name: "fn", FilePath: "web/src/a.ts", RepoPrefix: "web"}
	s.AddBatch([]*graph.Node{caller}, []*graph.Edge{
		{From: caller.ID, To: "web::builtin::ts::array::push", Kind: graph.EdgeCalls, FilePath: "web/src/a.ts", Line: 3},
		{From: caller.ID, To: "builtin::ts::string::split", Kind: graph.EdgeCalls, FilePath: "web/src/a.ts", Line: 4},
	})

	prefixed := s.GetNode("web::builtin::ts::array::push")
	require.NotNil(t, prefixed, "builtin edge target must exist as a node after AddBatch")
	assert.Equal(t, graph.KindBuiltin, prefixed.Kind)
	assert.Equal(t, "push", prefixed.Name)
	assert.Equal(t, "web", prefixed.RepoPrefix)

	solo := s.GetNode("builtin::ts::string::split")
	require.NotNil(t, solo, "solo-repo (unprefixed) stub form must materialize too")
	assert.Equal(t, graph.KindBuiltin, solo.Kind)
	assert.Equal(t, "", solo.RepoPrefix)

	// The edges must no longer read as orphans: every out-edge target
	// resolves to a live node.
	for _, e := range s.GetOutEdges(caller.ID) {
		require.NotNil(t, s.GetNode(e.To), "edge target %s must not dangle", e.To)
	}

	before := s.NodeCount()
	s.AddBatch(nil, []*graph.Edge{
		{From: caller.ID, To: "web::builtin::ts::array::push", Kind: graph.EdgeCalls, FilePath: "web/src/a.ts", Line: 9},
	})
	assert.Equal(t, before, s.NodeCount(), "re-adding an edge to a seen stub must not create nodes")
}
