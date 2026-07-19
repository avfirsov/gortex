package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_sqlite"
)

// newSqliteWatcher builds a repo of the given files, full-indexes it into a
// fresh sqlite store (so the per-file mtime rows a warm restart reads are
// actually persisted), and returns a Watcher wired to it ready to drive
// patchGraph / patchGraphNoResolve directly. The sqlite store implements the
// FileMtime{Writer,Reader,Deleter} capabilities the watcher's patch paths key
// off, so the in-memory graph backend's no-op behavior is covered separately.
func newSqliteWatcher(t *testing.T, files map[string]string) (dir string, idx *Indexer, w *Watcher, s *store_sqlite.Store) {
	t.Helper()
	dir = t.TempDir()
	for name, content := range files {
		writeTestFile(t, filepath.Join(dir, name), content)
	}
	s = openTestSqlite(t)
	idx = New(graph.Store(s), newTestRegistry(), config.Default().Index, zap.NewNop())
	idx.SetRootPath(dir)
	_, err := idx.IndexCtx(context.Background(), dir)
	require.NoError(t, err)
	w, err = NewWatcher(idx, config.WatchConfig{Enabled: true, DebounceMs: 10}, zap.NewNop())
	require.NoError(t, err)
	return dir, idx, w, s
}

// TestWatcher_DeletePatchPrunesPersistedMtime is the core regression: when the
// live watcher patches a deleted file it must drop that file's persisted mtime
// row. Before the fix EvictFile removed the file's nodes but left its mtime
// behind, so the next warm restart read the orphaned row back, found the path
// gone from disk, and treated it as a phantom deletion — forcing a scoped
// reconcile for an already-correct repo on every boot.
func TestWatcher_DeletePatchPrunesPersistedMtime(t *testing.T) {
	dir, idx, w, s := newSqliteWatcher(t, map[string]string{
		"gone.go": "package main\n\nfunc Gone() {}\n",
		"keep.go": "package main\n\nfunc Keep() {}\n",
	})

	before := s.LoadFileMtimes("")
	require.Contains(t, before, "gone.go", "the full index must persist gone.go's mtime")
	require.Contains(t, before, "keep.go", "the full index must persist keep.go's mtime")

	gonePath := filepath.Join(dir, "gone.go")
	require.NoError(t, os.Remove(gonePath))
	_ = w.patchGraph(gonePath, ChangeDeleted)

	after := s.LoadFileMtimes("")
	assert.NotContains(t, after, "gone.go",
		"a delete patch must prune the vanished file's persisted mtime row")
	assert.Contains(t, after, "keep.go",
		"a delete patch must not touch a sibling file's mtime row")

	// The in-memory map must be pruned too, or a later snapshot persist would
	// resurrect the store row from the stale in-memory entry.
	assert.NotContains(t, idx.FileMtimes(), "gone.go",
		"a delete patch must also drop the in-memory mtime entry")
}

// TestWatcher_StormDrainDeletePrunesPersistedMtime covers the batched delete
// path: storm-drain re-indexing routes deletes through patchGraphNoResolve,
// which must prune the persisted mtime just like the debounced patchGraph does.
func TestWatcher_StormDrainDeletePrunesPersistedMtime(t *testing.T) {
	dir, idx, w, s := newSqliteWatcher(t, map[string]string{
		"batch.go": "package main\n\nfunc Batch() {}\n",
	})
	require.Contains(t, s.LoadFileMtimes(""), "batch.go")

	path := filepath.Join(dir, "batch.go")
	require.NoError(t, os.Remove(path))
	w.patchGraphNoResolve(path, ChangeDeleted)

	assert.NotContains(t, s.LoadFileMtimes(""), "batch.go",
		"the storm-drain delete path must prune the persisted mtime too")
	assert.NotContains(t, idx.FileMtimes(), "batch.go",
		"the storm-drain delete path must drop the in-memory mtime entry")
}

// TestWatcher_ModifyPatchPersistsMtimeToStore is the other half of the
// contract: a modify patch must persist the file's advanced mtime so a warm
// restart does not re-detect the already-patched file as changed. A structural
// modify reindexes through IndexFile, whose recordFileMtime does the persist.
func TestWatcher_ModifyPatchPersistsMtimeToStore(t *testing.T) {
	dir, _, w, s := newSqliteWatcher(t, map[string]string{
		"main.go": "package main\n\nfunc First() {}\n",
	})
	path := filepath.Join(dir, "main.go")

	before := s.LoadFileMtimes("")["main.go"]
	require.NotZero(t, before)

	// Stamp a strictly later mtime so the advance is observable, and add a
	// function so the change is structural (forces the reindex path).
	future := time.Now().Add(2 * time.Second)
	writeTestFile(t, path, "package main\n\nfunc First() {}\n\nfunc Second() {}\n")
	require.NoError(t, os.Chtimes(path, future, future))
	_ = w.patchGraph(path, ChangeModified)

	info, statErr := os.Stat(path)
	require.NoError(t, statErr)
	after := s.LoadFileMtimes("")
	assert.Equal(t, info.ModTime().UnixNano(), after["main.go"],
		"a modify patch must persist the file's current on-disk mtime to the store")
	assert.Greater(t, after["main.go"], before,
		"the persisted mtime must advance past the modify")
}

// TestWatcher_DeleteEventForPresentFileKeepsMtime guards the interaction with
// reconcileKindWithDisk: a stale delete/rename event whose path is still a
// regular file is a replace/revert, downgraded to a modify — so the mtime must
// be refreshed, never pruned. This proves the prune fires only on a genuine
// on-disk deletion.
func TestWatcher_DeleteEventForPresentFileKeepsMtime(t *testing.T) {
	dir, _, w, s := newSqliteWatcher(t, map[string]string{
		"revert.go": "package main\n\nfunc Revert() {}\n",
	})
	path := filepath.Join(dir, "revert.go")
	require.Contains(t, s.LoadFileMtimes(""), "revert.go")

	_ = w.patchGraph(path, ChangeDeleted)

	assert.Contains(t, s.LoadFileMtimes(""), "revert.go",
		"a delete event for a still-present file (a revert) must not prune its mtime")
}

// TestWatcher_DeletePatchInMemoryBackendSkipsStore proves the store prune
// self-skips cleanly on a backend that implements no FileMtimeDeleter (the
// in-memory graph): no panic, no capability required, and the in-memory mtime
// entry is still pruned. Injecting a store whose DeleteFileMtimes returns an
// error is not done here — it would require a full fake graph.Store (AddBatch /
// GetFileNodes / EvictFile / ...) for a path whose only effect is a logged,
// non-fatal warn — so the realistic "store cannot persist" branch is the
// capability-absent one exercised below.
func TestWatcher_DeletePatchInMemoryBackendSkipsStore(t *testing.T) {
	dir, idx, w := inertTestWatcher(t, "solo.go", "package main\n\nfunc Solo() {}\n")
	path := filepath.Join(dir, "solo.go")
	require.Contains(t, idx.FileMtimes(), "solo.go")

	require.NoError(t, os.Remove(path))
	_ = w.patchGraph(path, ChangeDeleted)

	assert.NotContains(t, idx.FileMtimes(), "solo.go",
		"the in-memory mtime entry must be pruned even without a store deleter")
}
