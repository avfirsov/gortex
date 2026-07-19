package indexer

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_sqlite"
)

type countingContentProjectionStore struct {
	*store_sqlite.Store
	presenceCalls int
	wipeCalls     int
}

func (s *countingContentProjectionStore) ContentRepoHasRows(repoPrefix string) (bool, error) {
	s.presenceCalls++
	return s.Store.ContentRepoHasRows(repoPrefix)
}

func (s *countingContentProjectionStore) WipeContentFileInRepo(repoPrefix, filePath string) error {
	s.wipeCalls++
	return s.Store.WipeContentFileInRepo(repoPrefix, filePath)
}

func contentBodiesForRepo(t *testing.T, store graph.ContentSearcher, repoPrefix string) map[string]string {
	t.Helper()
	bodies := make(map[string]string)
	require.NoError(t, store.ScanContent(repoPrefix, func(nodeID, _ string, body string) bool {
		bodies[nodeID] = body
		return true
	}))
	return bodies
}

func TestPrepareFullContentFileWiperElidesColdDeletesAndPreservesWarmPartialRows(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "content.sqlite")
	base, err := store_sqlite.Open(dbPath)
	require.NoError(t, err)
	store := &countingContentProjectionStore{Store: base}

	recorded := make(map[string]struct{})
	wipe, projectionErr, ok := prepareFullContentFileWiper(store, "repoA", func(filePath string) {
		if filePath != "" {
			recorded[filePath] = struct{}{}
		}
	})
	require.True(t, ok)
	require.NoError(t, projectionErr)

	const coldFiles = 2048
	for i := 0; i < coldFiles; i++ {
		require.NoError(t, wipe(fmt.Sprintf("docs/%04d.md", i)))
	}
	require.Equal(t, 1, store.presenceCalls, "repo presence must be projected once, not once per file")
	require.Zero(t, store.wipeCalls, "an empty repo must not open empty per-file DELETE transactions")
	require.Len(t, recorded, coldFiles, "cold elision must still feed the authoritative end sweep")

	// Simulate a partial/crashed prior parse: content exists without any
	// repo completion state. The ownership projection alone must preserve the
	// crash-safe replacement behavior, including same-named sibling files.
	require.NoError(t, store.AppendContent("repoA", []graph.ContentFTSItem{
		{NodeID: "repoA/docs/a.md::0", FilePath: "docs/a.md", Body: "old partial body"},
	}))
	require.NoError(t, store.AppendContent("repoB", []graph.ContentFTSItem{
		{NodeID: "repoB/docs/a.md::0", FilePath: "docs/a.md", Body: "sibling body"},
	}))

	warmWipe, projectionErr, ok := prepareFullContentFileWiper(store, "repoA", func(string) {})
	require.True(t, ok)
	require.NoError(t, projectionErr)
	require.NoError(t, warmWipe("docs/a.md"))
	require.NoError(t, warmWipe("docs/b.md"))
	require.Equal(t, 2, store.presenceCalls, "each full parse gets one bounded repo projection")
	require.Equal(t, 2, store.wipeCalls, "warm/partial parses must wipe every affected file")
	require.Empty(t, contentBodiesForRepo(t, store, "repoA"))
	require.Equal(t, map[string]string{"repoB/docs/a.md::0": "sibling body"}, contentBodiesForRepo(t, store, "repoB"))

	require.NoError(t, store.AppendContent("repoA", []graph.ContentFTSItem{
		{NodeID: "repoA/docs/a.md::0", FilePath: "docs/a.md", Body: "fresh body"},
	}))
	require.NoError(t, base.Close())

	reopenedBase, err := store_sqlite.Open(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = reopenedBase.Close() })
	reopened := &countingContentProjectionStore{Store: reopenedBase}
	reopenWipe, projectionErr, ok := prepareFullContentFileWiper(reopened, "repoA", func(string) {})
	require.True(t, ok)
	require.NoError(t, projectionErr)
	require.NoError(t, reopenWipe("docs/a.md"))
	require.Equal(t, 1, reopened.presenceCalls)
	require.Equal(t, 1, reopened.wipeCalls, "persisted ownership must survive reopen")
	require.Empty(t, contentBodiesForRepo(t, reopened, "repoA"))
	require.Equal(t, map[string]string{"repoB/docs/a.md::0": "sibling body"}, contentBodiesForRepo(t, reopened, "repoB"))

	require.NoError(t, reopened.AppendContent("repoA", []graph.ContentFTSItem{
		{NodeID: "repoA/docs/a.md::0", FilePath: "docs/a.md", Body: "reopened replacement"},
	}))
	require.Equal(t, map[string]string{"repoA/docs/a.md::0": "reopened replacement"}, contentBodiesForRepo(t, reopened, "repoA"))
}

type failingContentPresenceStore struct {
	graph.ContentSearcher
	presenceCalls int
	wipeCalls     int
}

func (s *failingContentPresenceStore) ContentRepoHasRows(string) (bool, error) {
	s.presenceCalls++
	return false, fmt.Errorf("projection failed")
}

func (s *failingContentPresenceStore) WipeContentFileInRepo(string, string) error {
	s.wipeCalls++
	return nil
}

func TestPrepareFullContentFileWiperFailsConservative(t *testing.T) {
	store := &failingContentPresenceStore{}
	wipe, projectionErr, ok := prepareFullContentFileWiper(store, "repo", func(string) {})
	require.True(t, ok)
	require.EqualError(t, projectionErr, "projection failed")
	for i := 0; i < 32; i++ {
		require.NoError(t, wipe(fmt.Sprintf("%d.md", i)))
	}
	require.Equal(t, 1, store.presenceCalls)
	require.Equal(t, 32, store.wipeCalls, "projection errors must retain crash-safe replacement")
}
