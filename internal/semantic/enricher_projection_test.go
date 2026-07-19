package semantic

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

type semanticProjectionStore struct {
	graph.Store
	allNodes, allEdges, pointNodes, languageReads, edgeBatches int
}

func (s *semanticProjectionStore) AllNodes() []*graph.Node {
	s.allNodes++
	return s.Store.AllNodes()
}

func (s *semanticProjectionStore) AllEdges() []*graph.Edge {
	s.allEdges++
	return s.Store.AllEdges()
}

func (s *semanticProjectionStore) GetNode(id string) *graph.Node {
	s.pointNodes++
	return s.Store.GetNode(id)
}

func (s *semanticProjectionStore) GetNodesByLanguage(language string) []*graph.Node {
	s.languageReads++
	return s.Store.GetNodesByLanguage(language)
}

func (s *semanticProjectionStore) GetOutEdgesByNodeIDs(ids []string) map[string][]*graph.Edge {
	s.edgeBatches++
	return s.Store.GetOutEdgesByNodeIDs(ids)
}

func TestLanguageHelpersUsePredicateAndOneBatchedAdjacencyRead(t *testing.T) {
	g := graph.New()
	g.AddBatch([]*graph.Node{
		{ID: "go.go::Go", Kind: graph.KindFunction, Language: "go"},
		{ID: "py.py::Py", Kind: graph.KindFunction, Language: "python"},
		{ID: "go.go::Target", Kind: graph.KindFunction, Language: "go"},
	}, []*graph.Edge{
		{From: "go.go::Go", To: "go.go::Target", Kind: graph.EdgeCalls},
		{From: "py.py::Py", To: "go.go::Target", Kind: graph.EdgeCalls},
	})
	s := &semanticProjectionStore{Store: g}

	nodes := NodesByLanguage(s, "go")
	require.Len(t, nodes, 2)
	edges := EdgesByLanguage(s, "go")
	require.Len(t, edges, 1)
	require.Equal(t, "go.go::Go", edges[0].From)
	require.Zero(t, s.allNodes)
	require.Zero(t, s.allEdges)
	require.Zero(t, s.pointNodes)
	require.Equal(t, 2, s.languageReads)
	require.Equal(t, 1, s.edgeBatches)
}
