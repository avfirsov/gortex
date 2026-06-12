package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/persistence"
)

func TestFeedbackManager_RecordAndQuery(t *testing.T) {
	dir := t.TempDir()
	fm := newFeedbackManager(dir, "/tmp/test-repo")

	require.NoError(t, fm.Record(persistence.FeedbackEntry{
		Task:      "add MCP tool",
		Useful:    []string{"server.go::Server", "tools.go::register"},
		NotNeeded: []string{"types.go::NodeKind"},
		Missing:   []string{"enhancements.go::registerEnhancementTools"},
		Source:    "smart_context",
	}))

	require.NoError(t, fm.Record(persistence.FeedbackEntry{
		Task:      "fix search bug",
		Useful:    []string{"server.go::Server", "search.go::Search"},
		NotNeeded: []string{"types.go::NodeKind"},
		Source:    "smart_context",
	}))

	stats := fm.AggregatedStats("all", 10)
	assert.Equal(t, 2, stats["total_entries"])

	accuracy, ok := stats["accuracy"].(float64)
	require.True(t, ok)
	assert.Greater(t, accuracy, 0.5)
}

func TestFeedbackManager_ScoreComputation(t *testing.T) {
	fm := &feedbackManager{}

	// Record entries directly.
	for range 8 {
		_ = fm.Record(persistence.FeedbackEntry{
			Task:   "task",
			Useful: []string{"good.go::Func"},
			Source: "smart_context",
		})
	}
	for range 2 {
		_ = fm.Record(persistence.FeedbackEntry{
			Task:      "task",
			NotNeeded: []string{"good.go::Func"},
			Source:    "smart_context",
		})
	}

	// 8 useful, 2 not_needed → score = (8-2)/10 = 0.6
	score := fm.GetSymbolScore("good.go::Func")
	assert.InDelta(t, 0.6, score, 0.001)
}

func TestFeedbackManager_NegativeScore(t *testing.T) {
	fm := &feedbackManager{}

	for range 5 {
		_ = fm.Record(persistence.FeedbackEntry{
			Task:      "task",
			NotNeeded: []string{"bad.go::Func"},
			Source:    "smart_context",
		})
	}

	// 0 useful, 5 not_needed → score = -1.0
	score := fm.GetSymbolScore("bad.go::Func")
	assert.InDelta(t, -1.0, score, 0.001)
}

func TestFeedbackManager_EmptyStore(t *testing.T) {
	fm := &feedbackManager{}

	score := fm.GetSymbolScore("unknown.go::Func")
	assert.Equal(t, 0.0, score)

	assert.False(t, fm.HasData())
}

func TestFeedbackManager_Persistence(t *testing.T) {
	dir := t.TempDir()

	// Write with first manager.
	fm1 := newFeedbackManager(dir, "/tmp/repo")
	require.NoError(t, fm1.Record(persistence.FeedbackEntry{
		Task:   "task1",
		Useful: []string{"a.go::Foo"},
		Source: "smart_context",
	}))

	// Read with second manager (simulates server restart).
	fm2 := newFeedbackManager(dir, "/tmp/repo")
	assert.True(t, fm2.HasData())

	score := fm2.GetSymbolScore("a.go::Foo")
	assert.InDelta(t, 1.0, score, 0.001)
}

func TestFeedbackManager_MissedSymbols(t *testing.T) {
	fm := &feedbackManager{}

	for range 3 {
		_ = fm.Record(persistence.FeedbackEntry{
			Task:    "task",
			Missing: []string{"missed.go::Func", "rare.go::Func"},
			Source:  "smart_context",
		})
	}
	_ = fm.Record(persistence.FeedbackEntry{
		Task:    "task",
		Missing: []string{"missed.go::Func"},
		Source:  "smart_context",
	})

	// missed.go::Func has 4 misses, rare.go::Func has 3.
	symbols := fm.MissedSymbols(3)
	require.Len(t, symbols, 2)
	assert.Equal(t, "missed.go::Func", symbols[0])
	assert.Equal(t, "rare.go::Func", symbols[1])

	// With higher threshold, only missed.go qualifies.
	symbols = fm.MissedSymbols(4)
	require.Len(t, symbols, 1)
	assert.Equal(t, "missed.go::Func", symbols[0])
}

func TestFeedbackManager_FilterByToolSource(t *testing.T) {
	fm := &feedbackManager{}

	_ = fm.Record(persistence.FeedbackEntry{
		Task:   "task",
		Useful: []string{"a.go::Foo"},
		Source: "smart_context",
	})
	_ = fm.Record(persistence.FeedbackEntry{
		Task:   "task",
		Useful: []string{"b.go::Bar"},
		Source: "prefetch_context",
	})

	// Filter to smart_context only.
	stats := fm.AggregatedStats("smart_context", 10)
	assert.Equal(t, 1, stats["total_entries"])

	// All sources.
	stats = fm.AggregatedStats("all", 10)
	assert.Equal(t, 2, stats["total_entries"])
}
