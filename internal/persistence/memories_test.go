package persistence

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoriesPersistence_RoundTrip(t *testing.T) {
	cache := t.TempDir()
	dir := MemoriesDir(cache, "/tmp/repo-a")

	store := &MemoryStore{
		Version:  "test",
		RepoPath: "/tmp/repo-a",
		Entries: []MemoryEntry{
			{
				ID:          "mem-1",
				Timestamp:   time.Now().UTC(),
				UpdatedAt:   time.Now().UTC(),
				Body:        "lock invariant for Bar",
				Title:       "Bar lock invariant",
				Kind:        "invariant",
				Source:      "manual",
				Importance:  5,
				Confidence:  1.0,
				SymbolIDs:   []string{"pkg/foo.go::Bar"},
				FilePaths:   []string{"pkg/foo.go"},
				Tags:        []string{"invariant", "lock"},
				WorkspaceID: "ws-a",
				Pinned:      true,
			},
		},
	}
	require.NoError(t, SaveMemories(dir, store))

	got, err := LoadMemories(dir)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Len(t, got.Entries, 1)
	assert.Equal(t, "mem-1", got.Entries[0].ID)
	assert.Equal(t, "Bar lock invariant", got.Entries[0].Title)
	assert.Equal(t, "invariant", got.Entries[0].Kind)
	assert.Equal(t, 5, got.Entries[0].Importance)
	assert.True(t, got.Entries[0].Pinned)
	assert.Equal(t, []string{"pkg/foo.go::Bar"}, got.Entries[0].SymbolIDs)
}

func TestMemoriesPersistence_EmptyOnMissingFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	got, err := LoadMemories(dir)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Empty(t, got.Entries)
}

func TestMemoriesPersistence_TrimDropsLowImportanceFirst(t *testing.T) {
	in := make([]MemoryEntry, 0, 10)
	for i := range 10 {
		e := MemoryEntry{ID: memID(i), Importance: 4}
		// Mark a couple as importance=1 — those should be dropped first.
		if i == 2 || i == 4 {
			e.Importance = 1
		}
		// Pin one — must survive.
		if i == 7 {
			e.Pinned = true
			e.Importance = 1
		}
		in = append(in, e)
	}
	out := trimMemories(in, 6)
	require.Len(t, out, 6)

	pinnedOrHi := map[string]bool{}
	for _, e := range out {
		pinnedOrHi[e.ID] = true
	}
	assert.True(t, pinnedOrHi[memID(7)], "pinned[7] must survive even with importance=1")
	// Low-importance non-pinned (i=2, i=4) should both be dropped.
	assert.False(t, pinnedOrHi[memID(2)], "low-imp[2] must be dropped")
	assert.False(t, pinnedOrHi[memID(4)], "low-imp[4] must be dropped")
}

func TestMemoriesPersistence_TrimFallsBackToOldestNonPinned(t *testing.T) {
	// All entries have importance > 2 — the first pass can shed none,
	// so the fallback pass must shed the oldest non-pinned.
	in := make([]MemoryEntry, 0, 5)
	for i := range 5 {
		in = append(in, MemoryEntry{ID: memID(i), Importance: 5, Pinned: i == 4})
	}
	out := trimMemories(in, 3)
	require.Len(t, out, 3)
	// The pinned entry (i=4) and the newest non-pinned tail must survive.
	survived := map[string]bool{}
	for _, e := range out {
		survived[e.ID] = true
	}
	assert.True(t, survived[memID(4)], "pinned[4] must survive")
}

func TestMemoriesPersistence_TrimNoopUnderCap(t *testing.T) {
	in := []MemoryEntry{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	out := trimMemories(in, 10)
	assert.Equal(t, in, out)
}

func memID(i int) string {
	return "mem-" + string(rune('a'+i))
}
