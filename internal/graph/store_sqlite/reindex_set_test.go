package store_sqlite

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestReindexEdgesUsesBoundedSetStatementsAndFirstDuplicateWins(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "graph.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	const (
		fromID = "repo/caller.go::Caller"
		newTo  = "repo/target.go::Target"
	)
	oldEdges := make([]*graph.Edge, 0, reindexSetChunkSize+1)
	batch := make([]graph.EdgeReindex, 0, reindexSetChunkSize+1)
	for i := 0; i < reindexSetChunkSize+1; i++ {
		oldTo := fmt.Sprintf("repo/old-%03d.go::Target", i)
		oldEdges = append(oldEdges, &graph.Edge{
			From: fromID, To: oldTo, Kind: graph.EdgeCalls,
			FilePath: "repo/caller.go", Line: 7, Origin: "old",
		})
		batch = append(batch, graph.EdgeReindex{
			OldTo: oldTo,
			Edge: &graph.Edge{
				From: fromID, To: newTo, Kind: graph.EdgeCalls,
				FilePath: "repo/caller.go", Line: 7,
				Confidence: 0.8, ConfidenceLabel: "confirmed",
				Origin: fmt.Sprintf("candidate-%03d", i), Tier: "semantic",
			},
		})
	}
	store.AddBatch([]*graph.Node{{
		ID: fromID, Kind: graph.KindFunction, Name: "Caller",
		FilePath: "repo/caller.go", RepoPrefix: "repo",
	}}, oldEdges)

	beforeRevision := store.AnalysisMutationRevision()
	stats, err := store.reindexEdgesSetOriented(batch)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.selectStatements, "all affected identities fit one bounded prefetch")
	assert.Equal(t, 1, stats.deleteStatements)
	assert.Equal(t, 1, stats.insertStatements)
	assert.Equal(t, 2, stats.writeStatements(), "set-oriented writes replace 142 per-edge DELETE/INSERT statements")
	assert.Equal(t, len(batch), stats.deletedRows)
	assert.Equal(t, 1, stats.insertedRows)
	assert.Equal(t, beforeRevision+1, store.AnalysisMutationRevision())

	persisted := store.GetOutEdges(fromID)
	require.Len(t, persisted, 1)
	assert.Equal(t, newTo, persisted[0].To)
	assert.Equal(t, "candidate-000", persisted[0].Origin, "INSERT OR IGNORE ordering keeps the first converging payload")
	assert.InDelta(t, 0.8, persisted[0].Confidence, 0.0001)
	assert.Equal(t, "confirmed", persisted[0].ConfidenceLabel)
	assert.Equal(t, "semantic", persisted[0].Tier)

	buildMinimalAnalysisGeneration(t, store, "set-reindex-noop", 0, true)
	beforeRevision = store.AnalysisMutationRevision()
	stats, err = store.reindexEdgesSetOriented(batch)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.selectStatements)
	assert.Equal(t, 0, stats.writeStatements(), "an idempotent replay must not rewrite rows")
	assert.Equal(t, beforeRevision, store.AnalysisMutationRevision())
	_, found, err := store.LoadActiveAnalysisHeader(77)
	require.NoError(t, err)
	assert.True(t, found, "an idempotent replay must preserve active warm analysis")
}

func TestReindexEdgesRefreshDuplicateUsesLastWriteAndPreservesQuality(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "graph.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	const (
		fromID   = "repo/caller.go::Caller"
		targetID = "repo/target.go::Target"
		filePath = "repo/caller.go"
		line     = 11
	)
	store.AddBatch(nil, []*graph.Edge{{
		From: fromID, To: targetID, Kind: graph.EdgeCalls,
		FilePath: filePath, Line: line,
		Confidence: 0.2, ConfidenceLabel: "heuristic", Origin: "old", Tier: "syntax",
		Meta: map[string]any{"opaque": "old"},
	}})

	first := &graph.Edge{
		From: fromID, To: targetID, Kind: graph.EdgeCalls,
		FilePath: filePath, Line: line,
		Confidence: 0.6, ConfidenceLabel: "candidate", Origin: "first", Tier: "semantic",
		Meta: map[string]any{"opaque": "first"},
	}
	last := &graph.Edge{
		From: fromID, To: targetID, Kind: graph.EdgeCalls,
		FilePath: filePath, Line: line,
		Confidence: 0.91, ConfidenceLabel: "confirmed", Origin: "last", Tier: "compiler",
		CrossRepo: true,
		Meta: map[string]any{
			"opaque":                  "keep",
			"resolve_terminal":        true,
			"resolve_terminal_reason": "resolved",
		},
	}
	batch := []graph.EdgeReindex{
		{Edge: first, OldTo: targetID, RefreshIdentity: true, OldFilePath: filePath, OldLine: line},
		{Edge: last, OldTo: targetID, RefreshIdentity: true, OldFilePath: filePath, OldLine: line},
	}

	stats, err := store.reindexEdgesSetOriented(batch)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.selectStatements)
	assert.Equal(t, 1, stats.deleteStatements)
	assert.Equal(t, 1, stats.insertStatements)

	persisted := store.GetOutEdges(fromID)
	require.Len(t, persisted, 1)
	edge := persisted[0]
	assert.Equal(t, "last", edge.Origin)
	assert.Equal(t, "compiler", edge.Tier)
	assert.InDelta(t, 0.91, edge.Confidence, 0.0001)
	assert.Equal(t, "confirmed", edge.ConfidenceLabel)
	assert.True(t, edge.CrossRepo)
	assert.Equal(t, "keep", edge.Meta["opaque"])
	assert.Equal(t, true, edge.Meta["resolve_terminal"])
	assert.Equal(t, "resolved", edge.Meta["resolve_terminal_reason"])

	beforeRevision := store.AnalysisMutationRevision()
	stats, err = store.reindexEdgesSetOriented(batch)
	require.NoError(t, err)
	assert.Equal(t, 0, stats.writeStatements(), "transient first-write followed by identical last-write has no final state change")
	assert.Equal(t, beforeRevision, store.AnalysisMutationRevision())
}

func TestReindexEdgesNetCancellationAcrossSQLChunksIsNoop(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "graph.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	const (
		fromID       = "repo/caller.go::Caller"
		originalTo   = "unresolved::Original"
		intermediate = "unresolved::Intermediate"
		fillerFrom   = "repo/filler.go::Caller"
		fillerTo     = "repo/filler-target.go::Target"
	)
	original := &graph.Edge{
		From: fromID, To: originalTo, Kind: graph.EdgeCalls,
		FilePath: "repo/caller.go", Line: 17,
		Confidence: 0.85, ConfidenceLabel: "confirmed", Origin: "compiler", Tier: "semantic",
		Meta: map[string]any{"opaque": "preserve"},
	}
	filler := &graph.Edge{
		From: fillerFrom, To: fillerTo, Kind: graph.EdgeCalls,
		FilePath: "repo/filler.go", Line: 3, Origin: "filler",
	}
	store.AddBatch(nil, []*graph.Edge{original, filler})
	buildMinimalAnalysisGeneration(t, store, "set-reindex-net-noop", 0, true)
	beforeRevision := store.AnalysisMutationRevision()

	batch := make([]graph.EdgeReindex, 0, reindexSetChunkSize+1)
	intermediateEdge := *original
	intermediateEdge.To = intermediate
	batch = append(batch, graph.EdgeReindex{Edge: &intermediateEdge, OldTo: originalTo})
	for i := 0; i < reindexSetChunkSize-1; i++ {
		fillerCandidate := *filler
		batch = append(batch, graph.EdgeReindex{
			Edge:  &fillerCandidate,
			OldTo: fmt.Sprintf("repo/missing-%03d.go::Target", i),
		})
	}
	restored := *original
	batch = append(batch, graph.EdgeReindex{Edge: &restored, OldTo: intermediate})

	token := store.BeginMutationReceipt()
	stats, err := store.reindexEdgesSetOriented(batch)
	require.NoError(t, err)
	receipt := store.EndMutationReceipt(token)

	assert.Equal(t, 1, stats.selectStatements)
	assert.Equal(t, 0, stats.writeStatements(), "net cancellation must not issue transient writes")
	assert.Equal(t, 0, stats.deletedRows)
	assert.Equal(t, 0, stats.insertedRows)
	assert.Equal(t, beforeRevision, store.AnalysisMutationRevision())
	assert.True(t, receipt.Complete)
	assert.False(t, receipt.ResolutionRelevant, "a net-noop must not create resolver catch-up work")

	_, found, err := store.LoadActiveAnalysisHeader(77)
	require.NoError(t, err)
	assert.True(t, found, "a net-noop must preserve active warm analysis")
	persisted := store.GetOutEdges(fromID)
	require.Len(t, persisted, 1)
	assert.Equal(t, originalTo, persisted[0].To)
	assert.Equal(t, "preserve", persisted[0].Meta["opaque"])
}
