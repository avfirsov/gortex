package indexer

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/clones"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_sqlite"
)

type cloneProjectionCountingStore struct {
	graph.Store
	pager  graph.CloneCorpusPager
	writer graph.CloneCorpusWriter

	pageCalls  int
	writeCalls int
	writeRows  int
}

func newCloneProjectionCountingStore(store graph.Store) *cloneProjectionCountingStore {
	return &cloneProjectionCountingStore{
		Store:  store,
		pager:  store.(graph.CloneCorpusPager),
		writer: store.(graph.CloneCorpusWriter),
	}
}

func (s *cloneProjectionCountingStore) CloneCorpusPage(repoPrefix, after string, limit int) ([]graph.CloneCorpusRow, error) {
	s.pageCalls++
	return s.pager.CloneCorpusPage(repoPrefix, after, limit)
}

func (s *cloneProjectionCountingStore) BulkSetCloneCorpus(repoPrefix string, rows []graph.CloneCorpusRow) error {
	s.writeCalls++
	s.writeRows += len(rows)
	return s.writer.BulkSetCloneCorpus(repoPrefix, rows)
}

func TestSQLiteCloneCorpusSurvivesReloadAndWarmReplay(t *testing.T) {
	t.Setenv("GORTEX_SHADOW_MAX_FILES", "0") // exercise the direct SQLite cold path
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "main.go"), cloneRepoSource)
	dbPath := filepath.Join(t.TempDir(), "clone.sqlite")

	store, err := store_sqlite.Open(dbPath)
	require.NoError(t, err)
	idx := newTestIndexer(store)
	_, err = idx.Index(repo)
	require.NoError(t, err)
	require.Len(t, similarToEdges(store), 2, "cold SQLite index must emit the symmetric clone pair")
	require.NoError(t, store.Close())

	reopened, err := store_sqlite.Open(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = reopened.Close() })
	require.Len(t, similarToEdges(reopened), 2, "clone edges must survive SQLite reopen")

	for _, id := range []string{"main.go::processItems", "main.go::processRecords", "main.go::openAndScan"} {
		node := reopened.GetNode(id)
		require.NotNil(t, node)
		require.NotEmpty(t, node.Meta[cloneSigMetaKey], "flat clone_sig must hydrate into Meta after reopen")
		_, hasRawShingles := node.Meta[cloneShinglesMetaKey]
		require.False(t, hasRawShingles, "raw shingles must live only in the compact sidecar")
	}

	counted := newCloneProjectionCountingStore(reopened)
	page, err := counted.CloneCorpusPage("", "", cloneCorpusFinalizeBatch)
	require.NoError(t, err)
	require.Len(t, page, 3)
	for _, row := range page {
		require.True(t, row.Finalized)
		require.NotEmpty(t, row.Shingles)
		require.NotEmpty(t, row.Signature)
	}

	maintained := newIncrementalCloneIndex()
	maintained.Rebuild(counted, "")
	require.True(t, maintained.built)
	require.Equal(t, len(page), maintained.corpus)
	require.Len(t, maintained.shingles, len(page))
	seededPair := false
	for _, row := range page {
		probe, ok := clones.DecodeSignature(row.Signature)
		require.True(t, ok)
		if len(maintained.lsh.QueryPairs(clones.Item{
			ID: row.NodeID, Sig: probe, TokenCount: row.TokenCount,
		}, clones.DefaultThreshold)) > 0 {
			seededPair = true
			break
		}
	}
	require.True(t, seededPair, "rebuild must seed the persisted signatures into LSH")

	counted.pageCalls = 0
	counted.writeCalls = 0
	counted.writeRows = 0
	beforeEdges := reopened.EdgeCount()
	stats := detectClonesAndEmitEdgesCtx(context.Background(), counted, "", clones.DefaultThreshold)
	require.Equal(t, len(page), stats.Items)
	require.Equal(t, 1, counted.pageCalls, "warm finalized corpus needs one bounded page query")
	require.Zero(t, counted.writeCalls, "warm finalized corpus must not rewrite signatures")
	require.Zero(t, counted.writeRows)
	require.Equal(t, beforeEdges, reopened.EdgeCount(), "warm replay must be graph-idempotent")
}
