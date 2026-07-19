package graph

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGraphReindexEdgesKindChangeRepairsAdjacency(t *testing.T) {
	g := New()
	edge := &Edge{
		From:     "repo/caller.go::Caller",
		To:       UnresolvedMarker + "Target",
		Kind:     EdgeReads,
		FilePath: "repo/caller.go",
		Line:     17,
	}
	g.AddEdge(edge)

	oldTo := edge.To
	oldKind := edge.Kind
	edge.To = "repo/target.go::Target"
	edge.Kind = EdgeReferences
	g.ReindexEdges([]EdgeReindex{{Edge: edge, OldTo: oldTo, OldKind: oldKind}})

	out := g.GetOutEdges(edge.From)
	require.Len(t, out, 1)
	require.Same(t, edge, out[0])
	require.Equal(t, EdgeReferences, out[0].Kind)
	require.Empty(t, g.GetInEdges(oldTo))
	require.Equal(t, []*Edge{edge}, g.GetInEdges(edge.To))
	require.NoError(t, g.VerifyEdgeIdentities())
}
