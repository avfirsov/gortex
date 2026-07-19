package store_sqlite

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The override must reach the EFFECTIVE pragma on every physical connection
// class — writer and readers (the bulk connection draws from the writer
// pool) — not merely the requested DSN string. SQLite silently clamps or
// ignores invalid mmap requests, so the assertion reads PRAGMA mmap_size
// back from live connections.
func TestSQLiteMmapOverrideEffectiveOnAllConnections(t *testing.T) {
	effective := func(t *testing.T, s *Store) (writer, reader int64) {
		t.Helper()
		require.NoError(t, s.writerDB.QueryRow(`PRAGMA mmap_size`).Scan(&writer))
		require.NoError(t, s.db.QueryRow(`PRAGMA mmap_size`).Scan(&reader))
		return writer, reader
	}

	t.Run("default", func(t *testing.T) {
		t.Setenv("GORTEX_SQLITE_MMAP_MB", "")
		s, err := Open(filepath.Join(t.TempDir(), "mmap_default.sqlite"))
		require.NoError(t, err)
		defer s.Close()
		w, r := effective(t, s)
		assert.EqualValues(t, defaultSQLiteMmapBytes, w)
		assert.EqualValues(t, defaultSQLiteMmapBytes, r)
	})

	t.Run("override 512MiB", func(t *testing.T) {
		t.Setenv("GORTEX_SQLITE_MMAP_MB", "512")
		s, err := Open(filepath.Join(t.TempDir(), "mmap_512.sqlite"))
		require.NoError(t, err)
		defer s.Close()
		w, r := effective(t, s)
		assert.EqualValues(t, 512<<20, w)
		assert.EqualValues(t, 512<<20, r)
	})

	t.Run("zero disables mmap", func(t *testing.T) {
		t.Setenv("GORTEX_SQLITE_MMAP_MB", "0")
		s, err := Open(filepath.Join(t.TempDir(), "mmap_off.sqlite"))
		require.NoError(t, err)
		defer s.Close()
		w, r := effective(t, s)
		assert.Zero(t, w)
		assert.Zero(t, r)
	})

	t.Run("garbage and negative fail open to default", func(t *testing.T) {
		for _, raw := range []string{"not-a-number", "-64", "1e9"} {
			t.Setenv("GORTEX_SQLITE_MMAP_MB", raw)
			assert.EqualValues(t, defaultSQLiteMmapBytes, sqliteMmapBytes(), "input %q", raw)
		}
	})

	t.Run("overflow saturates via clamping", func(t *testing.T) {
		// SQLite clamps the effective window to its compile-time maximum;
		// the request must not wrap negative on our side.
		t.Setenv("GORTEX_SQLITE_MMAP_MB", "9007199254740")
		assert.Positive(t, sqliteMmapBytes())
	})
}
