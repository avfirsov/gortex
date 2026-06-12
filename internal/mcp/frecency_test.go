package mcp

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/persistence"
)

func TestFrecency_ScoreDecaysOverTime(t *testing.T) {
	ft := &frecencyTracker{mode: ModeAI, now: func() int64 { return 1000 }}
	ft.Record("sym-a")

	// Same instant: full weight (1.0).
	assert.InDelta(t, 1.0, ft.Score("sym-a"), 0.0001)

	// Advance clock by one full half-life (3 days @ AI mode).
	ft.now = func() int64 { return 1000 + int64(3*86400) }
	assert.InDelta(t, 0.5, ft.Score("sym-a"), 0.01, "one half-life should halve the score")

	// Six days out: quarter weight.
	ft.now = func() int64 { return 1000 + int64(6*86400) }
	assert.InDelta(t, 0.25, ft.Score("sym-a"), 0.01)
}

func TestFrecency_MultipleAccessesAddUp(t *testing.T) {
	ft := &frecencyTracker{mode: ModeAI, now: func() int64 { return 1000 }}
	ft.Record("sym-a")
	ft.Record("sym-a")
	ft.Record("sym-a")
	// All at t=0 → each contributes weight 1 → total = 3.
	assert.InDelta(t, 3.0, ft.Score("sym-a"), 0.0001)
}

func TestFrecency_HumanModeSlowerDecay(t *testing.T) {
	now := int64(1000)
	ai := &frecencyTracker{mode: ModeAI, now: func() int64 { return now }}
	human := &frecencyTracker{mode: ModeHuman, now: func() int64 { return now }}
	ai.Record("x")
	human.Record("x")

	// Move forward 5 days.
	now += int64(5 * 86400)
	aiScore := ai.Score("x")
	humanScore := human.Score("x")
	assert.Less(t, aiScore, humanScore, "AI mode decays faster than human")
}

func TestFrecency_HistoryBounded(t *testing.T) {
	ft := &frecencyTracker{mode: ModeAI, now: func() int64 { return 1 }}
	cap := persistence.MaxFrecencyAccesses()
	for i := 0; i < cap*3; i++ {
		ft.Record("sym")
	}
	ft.mu.Lock()
	defer ft.mu.Unlock()
	require.Len(t, ft.store.Symbols, 1)
	assert.LessOrEqual(t, len(ft.store.Symbols[0].Times), cap)
}

func TestFrecency_BoostCappedAtMax(t *testing.T) {
	ft := &frecencyTracker{mode: ModeAI, now: func() int64 { return 1 }}
	// Record way more accesses than the cap — BoostFor must saturate, not
	// grow unboundedly.
	for i := 0; i < 100; i++ {
		ft.Record("hot")
	}
	b := ft.BoostFor("hot")
	assert.LessOrEqual(t, b, maxFrecencyBoost+0.01)
	assert.Greater(t, b, 1.0)
}

func TestFrecency_UnknownSymbolReturnsUnitBoost(t *testing.T) {
	ft := &frecencyTracker{mode: ModeAI, now: func() int64 { return 1 }}
	assert.Equal(t, 0.0, ft.Score("missing"))
	assert.Equal(t, 1.0, ft.BoostFor("missing"))
}

func TestFrecency_Persistence(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "repo")
	cacheDir := filepath.Join(dir, "cache")

	ft := newFrecencyTracker(cacheDir, repoPath, ModeAI)
	ft.now = func() int64 { return 1 }
	ft.Record("alpha")
	ft.Record("beta")
	ft.Record("alpha")
	require.True(t, ft.HasData())

	// Reload: history survives.
	ft2 := newFrecencyTracker(cacheDir, repoPath, ModeAI)
	ft2.now = func() int64 { return 1 }
	assert.InDelta(t, 2.0, ft2.Score("alpha"), 0.001)
	assert.InDelta(t, 1.0, ft2.Score("beta"), 0.001)
}

func TestFrecency_NilSafe(t *testing.T) {
	var ft *frecencyTracker
	ft.Record("x")
	assert.Equal(t, 0.0, ft.Score("x"))
	assert.Equal(t, 1.0, ft.BoostFor("x"))
	assert.False(t, ft.HasData())
}
