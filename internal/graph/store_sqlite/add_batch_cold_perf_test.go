package store_sqlite

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func coldBatchFixture(count int) ([]*graph.Node, []*graph.Edge) {
	nodes := make([]*graph.Node, count)
	edges := make([]*graph.Edge, count)
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("repo/f.go::N%05d", i)
		nodes[i] = &graph.Node{
			ID: id, Kind: graph.KindFunction, Name: fmt.Sprintf("N%05d", i),
			QualName: id, FilePath: "repo/f.go", RepoPrefix: "repo",
		}
		edges[i] = &graph.Edge{
			From: id, To: fmt.Sprintf("repo/f.go::N%05d", (i+1)%count),
			Kind: graph.EdgeCalls, FilePath: "repo/f.go", Line: i + 1,
		}
	}
	return nodes, edges
}

func TestAddBatchUsesRuntimeVariableLimitWithBoundedStatements(t *testing.T) {
	// Statement-shape assertions below are specific to the PLACEHOLDER
	// writer's runtime-variable-limit chunking; the JSONB fast path uses two
	// payload binds per statement and is covered by its own parity tests.
	t.Setenv("GORTEX_SQLITE_JSONB_INGEST", "0")
	store := openReindexReceiptTestStore(t)
	nodes, edges := coldBatchFixture(1025)

	stats, err := store.addBatchSetOriented(nodes, edges)
	require.NoError(t, err)
	require.Equal(t, len(nodes), stats.nodeRowsChanged)
	require.Equal(t, len(edges), stats.edgeRowsInserted)

	nodeRows := batchRowsForVariableLimit(store.batchVariableLimit, nodeInsertParams, nodeInsertMaxChunkSize)
	edgeRows := batchRowsForVariableLimit(store.batchVariableLimit, edgeInsertParams, edgeInsertMaxChunkSize)
	require.Equal(t, (len(nodes)+nodeRows-1)/nodeRows, stats.nodeStatements)
	require.Equal(t, (len(edges)+edgeRows-1)/edgeRows, stats.edgeStatements)
	require.Less(t, stats.nodeStatements+stats.edgeStatements, 16,
		"the modern SQLite limit should collapse the former ~45 statements")
}

func TestAddBatchAutomaticallyFallsBackToWriterVariableLimit(t *testing.T) {
	// This test exercises the PLACEHOLDER writer's variable-limit fallback;
	// the JSONB fast path binds two payloads and never trips the limit.
	t.Setenv("GORTEX_SQLITE_JSONB_INGEST", "0")
	store := openReindexReceiptTestStore(t)
	require.True(t, store.BeginCoordinatedBulkLoad())
	require.NotNil(t, store.bulkConn)

	previous, err := modernsqlite.Limit(store.bulkConn, int(sqlite3.SQLITE_LIMIT_VARIABLE_NUMBER), 1000)
	require.NoError(t, err)
	store.batchVariableLimit = sqliteBatchVariableHardCap // deliberately stale/high

	nodes, _ := coldBatchFixture(300)
	stats, err := store.addBatchSetOriented(nodes, nil)
	require.NoError(t, err)
	require.Equal(t, len(nodes), stats.nodeRowsChanged)
	require.LessOrEqual(t, store.batchVariableLimit, 1000)
	require.Less(t, stats.nodeStatements, len(nodes), "fallback must remain set-oriented")

	_, err = modernsqlite.Limit(store.bulkConn, int(sqlite3.SQLITE_LIMIT_VARIABLE_NUMBER), previous)
	require.NoError(t, err)
	require.NoError(t, store.EndCoordinatedBulkLoad())
}

func TestLargeAddBatchRollbackRemovesEarlierSuccessfulChunks(t *testing.T) {
	store := openReindexReceiptTestStore(t)
	store.batchVariableLimit = sqliteBatchVariableHardCap
	nodes, _ := coldBatchFixture(nodeInsertMaxChunkSize + 1)
	nodes[len(nodes)-1].Meta = map[string]any{"unsupported": make(chan int)}

	token := store.BeginMutationReceipt()
	_, err := store.addBatchSetOriented(nodes, nil)
	require.Error(t, err)
	receipt := store.EndMutationReceipt(token)

	require.Zero(t, store.NodeCount(), "the first SQL chunk must roll back with the failing tail")
	require.True(t, receipt.Complete)
	require.False(t, receipt.ResolutionRelevant)
	require.Empty(t, receipt.ResolutionFiles())
}

func TestCoordinatedColdFinalizationUsesBoundedFTSMergeAndSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cold.db")
	store, err := Open(path)
	require.NoError(t, err)
	require.True(t, store.BeginCoordinatedBulkLoad())

	var events []bulkFinalizeEvent
	store.bulkFinalizeObserver = func(event bulkFinalizeEvent) {
		events = append(events, event)
	}
	nodes := []*graph.Node{
		{ID: "repo/f.go::ColdOne", Kind: graph.KindFunction, Name: "ColdOne", QualName: "repo.ColdOne", FilePath: "repo/f.go", RepoPrefix: "repo"},
		{ID: "repo/f.go::ColdTwo", Kind: graph.KindFunction, Name: "ColdTwo", QualName: "repo.ColdTwo", FilePath: "repo/f.go", RepoPrefix: "repo"},
	}
	store.AddBatch(nodes, nil)
	require.NoError(t, store.BatchUpsertSymbolFTS([]graph.SymbolFTSItem{
		{NodeID: nodes[0].ID, Tokens: "coldmergetoken one"},
		{NodeID: nodes[1].ID, Tokens: "coldmergetoken two"},
	}))
	require.NoError(t, store.BuildSymbolIndex())
	require.NoError(t, store.BuildContentIndex())
	require.NoError(t, store.EndCoordinatedBulkLoad())

	indexEvents := make(map[string]bool)
	mergeEvents := make(map[string]bool)
	checkpointEvents := 0
	for _, event := range events {
		require.NoError(t, event.Err)
		switch event.Stage {
		case "index":
			indexEvents[event.Name] = true
		case "fts_merge":
			mergeEvents[event.Name] = true
		case "checkpoint":
			checkpointEvents++
		}
	}
	require.Len(t, indexEvents, len(bulkDroppableIndexes))
	require.Equal(t, map[string]bool{"symbol_fts": true, "content_fts": true}, mergeEvents)
	require.Equal(t, 1, checkpointEvents)

	hits, err := store.SearchSymbols("coldmergetoken", 10)
	require.NoError(t, err)
	require.Len(t, hits, 2)
	require.NoError(t, store.Close())

	reopened, err := Open(path)
	require.NoError(t, err)
	hits, err = reopened.SearchSymbols("coldmergetoken", 10)
	require.NoError(t, err)
	require.Len(t, hits, 2)
	var ftsRows, ownerRows int
	require.NoError(t, reopened.db.QueryRow(`SELECT COUNT(*) FROM symbol_fts`).Scan(&ftsRows))
	require.NoError(t, reopened.db.QueryRow(`SELECT COUNT(*) FROM symbol_fts_rowid`).Scan(&ownerRows))
	require.Equal(t, 2, ftsRows)
	require.Equal(t, ftsRows, ownerRows)
	require.NoError(t, reopened.Close())
}
