package indexer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
	"github.com/zzet/gortex/internal/search"
)

// upgradeSpawnCount is an in-package accessor kept as a function so
// the test doesn't have to export a field just to read the counter.
func upgradeSpawnCount(idx *Indexer) int {
	idx.upgradeSpawnedMu.Lock()
	defer idx.upgradeSpawnedMu.Unlock()
	return idx.upgradeSpawned
}

// Second and later calls to upgradeSearchToBleve must be a no-op when
// the text backend is already Bleve. Under the bug, each call would
// rebuild a full Bleve index and Swap it in — observable as a new
// Bleve pointer identity. The defensive early-return at the top of
// the function keeps the pointer stable.
func TestUpgradeSearchToBleve_IdempotentOnSecondCall(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main

func Alpha() {}
`), 0o644))

	g := graph.New()
	reg := parser.NewRegistry()
	reg.Register(languages.NewGoExtractor())

	cfg := config.Default().Index
	cfg.Workers = 1
	cfg.SkipSearch = config.DefaultSkipSearch()

	idx := New(g, reg, cfg, zap.NewNop())
	idx.SetEmbedder(stubEmbedder{})

	_, err := idx.Index(dir)
	require.NoError(t, err)

	sw, ok := idx.Search().(*search.Swappable)
	require.True(t, ok)

	// First upgrade: text side transitions BM25 → Bleve. We don't
	// care about identity here — just capture the post-upgrade
	// Bleve pointer so we can compare to the second run.
	idx.upgradeSearchToBleve(idx.snapshotBleveEntries())
	firstHybrid, ok := sw.Inner().(*search.HybridBackend)
	require.True(t, ok, "after first upgrade, inner must be Hybrid(Bleve, Vector)")
	firstBleve, ok := firstHybrid.TextBackend().(*search.BleveBackend)
	require.True(t, ok)

	// Second upgrade must early-return. No new Bleve is built, no
	// Swap happens.
	idx.upgradeSearchToBleve(idx.snapshotBleveEntries())
	secondHybrid, ok := sw.Inner().(*search.HybridBackend)
	require.True(t, ok, "inner should still be Hybrid after no-op second call")
	secondBleve, ok := secondHybrid.TextBackend().(*search.BleveBackend)
	require.True(t, ok)

	assert.Same(t, firstHybrid, secondHybrid,
		"second upgrade must not replace the Hybrid wrapper")
	assert.Same(t, firstBleve, secondBleve,
		"second upgrade must not rebuild Bleve")
}

// Only one upgrade goroutine may spawn per Indexer lifetime, even if
// IndexCtx runs many times past AutoThreshold (as it does in
// multi-repo warmup). Uses a low override threshold so a tiny test
// corpus can trigger it.
func TestIndexCtx_SpawnsUpgradeAtMostOnce(t *testing.T) {
	dir := t.TempDir()
	// Two Go files, each with several funcs — enough that one index
	// pass produces a handful of search docs. The threshold overrides
	// below ensure we cross it.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte(`package main

func A1() {}
func A2() {}
func A3() {}
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.go"), []byte(`package main

func B1() {}
func B2() {}
func B3() {}
`), 0o644))

	g := graph.New()
	reg := parser.NewRegistry()
	reg.Register(languages.NewGoExtractor())

	cfg := config.Default().Index
	cfg.Workers = 1
	cfg.SkipSearch = config.DefaultSkipSearch()

	idx := New(g, reg, cfg, zap.NewNop())
	idx.SetEmbedder(stubEmbedder{})

	// Temporarily force the threshold to 1 so any non-empty index
	// triggers the upgrade branch. Restored after the test.
	orig := search.AutoThreshold
	defer func() { search.AutoThreshold = orig }()
	search.AutoThreshold = 1

	for range 5 {
		_, err := idx.Index(dir)
		require.NoError(t, err)
	}

	assert.Equal(t, 1, upgradeSpawnCount(idx),
		"upgradeOnce must gate all post-threshold Index calls to one spawn")
}
