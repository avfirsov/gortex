package indexer

import (
	"context"
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

// stubEmbedder is a deterministic minimal embedder that lets the
// indexer wire up a HybridBackend in tests without pulling in the
// static-vector asset or a real ONNX runtime. It emits a 4-dim
// vector whose first element is the text length — enough for the
// vector backend to accept the adds and for Search to return
// something non-empty.
type stubEmbedder struct{}

func (stubEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	return []float32{float32(len(text)), 0, 0, 0}, nil
}

func (stubEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = []float32{float32(len(t)), 0, 0, 0}
	}
	return out, nil
}

func (stubEmbedder) Dimensions() int { return 4 }
func (stubEmbedder) Close() error    { return nil }

// upgradeSearchToBleve must rewrap the new Bleve in a HybridBackend
// carrying the pre-existing vector + embedder. Without this, swapping
// in a raw *BleveBackend causes Swap to Close() the old Hybrid (which
// closes only the text side) and leaves every downstream query
// silently degraded to BM25-only, with vectorBytes reporting as 0 in
// the daemon status. Regression guard for the vector-index
// destruction bug that went live in v0.10.0.
func TestUpgradeSearchToBleve_PreservesVectorIndex(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main

func Alpha() {}
func Beta() {}
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

	// Sanity: post-index, inner is Hybrid(BM25, Vector).
	sw, ok := idx.Search().(*search.Swappable)
	require.True(t, ok, "indexer search is always a Swappable")
	preHybrid, ok := sw.Inner().(*search.HybridBackend)
	require.True(t, ok, "buildSearchIndex should have produced a HybridBackend")
	require.NotNil(t, preHybrid.VectorIndex(), "vector backend must be attached")
	preVector := preHybrid.VectorIndex()
	preVectorBytes := preHybrid.VectorSizeBytes()
	require.Greater(t, preVectorBytes, uint64(0), "vector index should have content")

	// Force the upgrade directly — don't go through the AutoThreshold
	// check. Under the bug the resulting inner is a raw *BleveBackend.
	// Under the fix it is a new Hybrid wrapping the same vector.
	idx.upgradeSearchToBleve(idx.snapshotBleveEntries())

	postHybrid, ok := sw.Inner().(*search.HybridBackend)
	require.True(t, ok, "post-upgrade inner must still be *HybridBackend")
	assert.Same(t, preVector, postHybrid.VectorIndex(),
		"vector backend pointer must be preserved across the upgrade")
	assert.Equal(t, preVectorBytes, postHybrid.VectorSizeBytes(),
		"vector byte count must be unchanged — Hybrid.Close does not touch the vector")

	// Text side must be the new Bleve, not the old BM25.
	_, isBleve := postHybrid.TextBackend().(*search.BleveBackend)
	assert.True(t, isBleve, "text side must be the upgraded Bleve backend")
}
