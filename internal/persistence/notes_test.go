package persistence

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotesPersistence_RoundTrip(t *testing.T) {
	cache := t.TempDir()
	dir := NotesDir(cache, "/tmp/repo-a")

	store := &NoteStore{
		Version:  "test",
		RepoPath: "/tmp/repo-a",
		Entries: []NoteEntry{
			{
				ID:        "nt-1",
				Timestamp: time.Now().UTC(),
				UpdatedAt: time.Now().UTC(),
				SessionID: "sess-1",
				Body:      "decision: switch to fastpath",
				SymbolID:  "pkg/foo.go::Bar",
				Tags:      []string{"decision"},
				AutoLinks: []string{"pkg/foo.go::Bar"},
				Pinned:    true,
			},
		},
	}
	require.NoError(t, SaveNotes(dir, store))

	got, err := LoadNotes(dir)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Len(t, got.Entries, 1)
	assert.Equal(t, "nt-1", got.Entries[0].ID)
	assert.Equal(t, "pkg/foo.go::Bar", got.Entries[0].SymbolID)
	assert.True(t, got.Entries[0].Pinned)
}

func TestNotesPersistence_EmptyOnMissingFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	got, err := LoadNotes(dir)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Empty(t, got.Entries)
}

func TestNotesPersistence_TrimDropsOldestUnpinned(t *testing.T) {
	in := make([]NoteEntry, 0, 10)
	for i := range 10 {
		in = append(in, NoteEntry{ID: noteID(i), Pinned: i%5 == 0})
	}
	out := trimNotes(in, 6)
	require.Len(t, out, 6)

	// Both pinned entries (i=0, i=5) must survive.
	pinnedIDs := map[string]bool{}
	for _, e := range out {
		if e.Pinned {
			pinnedIDs[e.ID] = true
		}
	}
	assert.True(t, pinnedIDs[noteID(0)], "pinned[0] must survive")
	assert.True(t, pinnedIDs[noteID(5)], "pinned[5] must survive")

	// The newest entries should be present (LIFO preserves the tail).
	assert.Equal(t, noteID(9), out[len(out)-1].ID)
}

func TestNotesPersistence_TrimNoopUnderCap(t *testing.T) {
	in := []NoteEntry{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	out := trimNotes(in, 10)
	assert.Equal(t, in, out)
}

func noteID(i int) string {
	return "nt-" + string(rune('a'+i))
}
