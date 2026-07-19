package resolver

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

// resolverProjectionStore intentionally exposes only graph.Store's required
// methods. Optional SQLite/Graph projection capabilities are hidden so these
// tests exercise the adapter fallback paths.
type resolverProjectionStore struct {
	graph.Store
	allNodes, allEdges, pointNodes, nodeBatches, addEdges, addBatches int
}

func (s *resolverProjectionStore) AllNodes() []*graph.Node {
	s.allNodes++
	return s.Store.AllNodes()
}

func (s *resolverProjectionStore) AllEdges() []*graph.Edge {
	s.allEdges++
	return s.Store.AllEdges()
}

func (s *resolverProjectionStore) GetNode(id string) *graph.Node {
	s.pointNodes++
	return s.Store.GetNode(id)
}

func (s *resolverProjectionStore) GetNodesByIDs(ids []string) map[string]*graph.Node {
	s.nodeBatches++
	return s.Store.GetNodesByIDs(ids)
}

func (s *resolverProjectionStore) AddEdge(edge *graph.Edge) {
	s.addEdges++
	s.Store.AddEdge(edge)
}

func (s *resolverProjectionStore) AddBatch(nodes []*graph.Node, edges []*graph.Edge) {
	s.addBatches++
	s.Store.AddBatch(nodes, edges)
}

func TestStructuralParentFallbackUsesPredicatesAndOneEndpointBatch(t *testing.T) {
	g := graph.New()
	g.AddBatch([]*graph.Node{
		{ID: "child", Kind: graph.KindType},
		{ID: "parent", Kind: graph.KindInterface},
		{ID: "fn", Kind: graph.KindFunction},
	}, []*graph.Edge{
		{From: "child", To: "parent", Kind: graph.EdgeImplements, Origin: graph.OriginASTInferred},
		{From: "fn", To: "child", Kind: graph.EdgeCalls},
	})
	s := &resolverProjectionStore{Store: g}

	rows := structuralParentEdges(s)
	require.Len(t, rows, 1)
	require.Equal(t, "child", rows[0].FromID)
	require.Equal(t, "parent", rows[0].ToID)
	require.Zero(t, s.allNodes)
	require.Zero(t, s.allEdges)
	require.Zero(t, s.pointNodes)
	require.Equal(t, 1, s.nodeBatches)
}

func TestKMPPassUsesKindPredicatesAndOneBatchedWrite(t *testing.T) {
	g := graph.New()
	g.AddBatch([]*graph.Node{
		{ID: "common.kt::Clock", Kind: graph.KindType, Name: "Clock", Language: "kotlin", Meta: map[string]any{"kmp_role": "expect"}},
		{ID: "android.kt::Clock", Kind: graph.KindType, Name: "Clock", Language: "kotlin", Meta: map[string]any{"kmp_role": "actual"}},
		{ID: "other", Kind: graph.KindFile, Name: "other"},
	}, nil)
	s := &resolverProjectionStore{Store: g}

	require.Equal(t, 1, ResolveKMPExpectActual(s))
	require.Zero(t, s.allNodes)
	require.Zero(t, s.allEdges)
	require.Zero(t, s.addEdges)
	require.Equal(t, 1, s.addBatches)
}
