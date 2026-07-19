package store_sqlite

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

func TestSetEdgeProvenanceBatchUsesBoundedSetOrientedStatements(t *testing.T) {
	store := openMutationReceiptStore(t)
	nodes := []*graph.Node{{ID: "repo/caller.go::Caller", Kind: graph.KindFunction, Name: "Caller"}}
	edges := make([]*graph.Edge, provenanceSelectChunkSize+1)
	for i := range edges {
		to := fmt.Sprintf("repo/t%d.go::T", i)
		nodes = append(nodes, &graph.Node{ID: to, Kind: graph.KindFunction, Name: "T"})
		edges[i] = &graph.Edge{
			From: nodes[0].ID, To: to, Kind: graph.EdgeCalls,
			FilePath: "repo/caller.go", Line: i + 1, Origin: "syntax", Tier: "syntax",
		}
	}
	store.AddBatch(nodes, edges)

	updates := make([]graph.EdgeProvenanceUpdate, 0, len(edges)+3)
	for _, edge := range edges {
		updates = append(updates, graph.EdgeProvenanceUpdate{Edge: edge, NewOrigin: "go-types"})
	}
	// Ordered duplicate transitions count exactly as the old per-edge loop and
	// the last transition wins. Nil and missing identities remain no-ops.
	updates = append(updates,
		graph.EdgeProvenanceUpdate{Edge: edges[0], NewOrigin: "lsp"},
		graph.EdgeProvenanceUpdate{},
		graph.EdgeProvenanceUpdate{Edge: &graph.Edge{From: "missing", To: "missing", Kind: graph.EdgeCalls}, NewOrigin: "missing"},
	)

	changed, statements, err := store.setEdgeProvenanceBatchSetOriented(updates)
	require.NoError(t, err)
	require.Equal(t, len(edges)+1, changed)
	require.Equal(t, 4, statements, "181 unique edges require two SELECTs and two UPDATEs")
	require.Equal(t, "lsp", edges[0].Origin)
	require.Equal(t, graph.ResolvedBy("lsp"), edges[0].Tier)
	persisted := store.GetEdgeCandidates(
		[]graph.EdgeEndpoint{{From: edges[0].From, To: edges[0].To}}, nil,
	).EndpointKind(edges[0].From, edges[0].To, edges[0].Kind)
	require.NotNil(t, persisted)
	require.Equal(t, "lsp", persisted.Origin)
	require.Equal(t, graph.ResolvedBy("lsp"), persisted.Tier)

	buildMinimalAnalysisGeneration(t, store, "provenance-noop", 0, true)
	before := store.AnalysisMutationRevision()
	idempotent := make([]graph.EdgeProvenanceUpdate, len(edges))
	for i, edge := range edges {
		idempotent[i] = graph.EdgeProvenanceUpdate{Edge: edge, NewOrigin: edge.Origin}
	}
	changed, statements, err = store.setEdgeProvenanceBatchSetOriented(idempotent)
	require.NoError(t, err)
	require.Zero(t, changed)
	require.Equal(t, 2, statements, "idempotent batch performs only bounded origin reads")
	require.Equal(t, before, store.AnalysisMutationRevision())
	_, found, err := store.LoadActiveAnalysisHeader(77)
	require.NoError(t, err)
	require.True(t, found, "idempotent provenance must preserve active analysis")
}
