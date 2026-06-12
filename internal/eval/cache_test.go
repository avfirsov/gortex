package eval

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCache_EmptyDirDefaultsToHome(t *testing.T) {
	c, err := NewCache("", "v1.0.0")
	require.NoError(t, err)

	home, err := os.UserHomeDir()
	require.NoError(t, err)

	expected := filepath.Join(home, ".gortex-eval-cache")
	assert.Equal(t, expected, c.dir)
}

func TestCheck_ReturnsFalseForNonExistentEntry(t *testing.T) {
	c, err := NewCache(t.TempDir(), "v1.0.0")
	require.NoError(t, err)

	assert.False(t, c.Check("myrepo", "abc123"))
}

func TestStoreAndCheck(t *testing.T) {
	cacheDir := t.TempDir()
	c, err := NewCache(cacheDir, "v1.0.0")
	require.NoError(t, err)

	// Create a fake index directory with some content.
	indexDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(indexDir, "graph.bin"), []byte("graph-data"), 0o644))

	require.NoError(t, c.Store("myrepo", "abc123", indexDir))
	assert.True(t, c.Check("myrepo", "abc123"))
}

func TestStoreAndLoad(t *testing.T) {
	cacheDir := t.TempDir()
	c, err := NewCache(cacheDir, "v1.0.0")
	require.NoError(t, err)

	indexDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(indexDir, "graph.bin"), []byte("graph-data"), 0o644))

	require.NoError(t, c.Store("myrepo", "abc123", indexDir))

	path, err := c.Load("myrepo", "abc123")
	require.NoError(t, err)

	expected := filepath.Join(cacheDir, "myrepo_abc123")
	assert.Equal(t, expected, path)

	// Verify the copied content is intact.
	data, err := os.ReadFile(filepath.Join(path, "graph.bin"))
	require.NoError(t, err)
	assert.Equal(t, "graph-data", string(data))
}

func TestStoreAndValidate_MatchingVersion(t *testing.T) {
	cacheDir := t.TempDir()
	c, err := NewCache(cacheDir, "v1.0.0")
	require.NoError(t, err)

	indexDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(indexDir, "graph.bin"), []byte("data"), 0o644))

	require.NoError(t, c.Store("myrepo", "abc123", indexDir))
	assert.True(t, c.Validate("myrepo", "abc123"))
}

func TestValidate_ReturnsFalseForMismatchedVersion(t *testing.T) {
	cacheDir := t.TempDir()
	c1, err := NewCache(cacheDir, "v1.0.0")
	require.NoError(t, err)

	indexDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(indexDir, "graph.bin"), []byte("data"), 0o644))

	require.NoError(t, c1.Store("myrepo", "abc123", indexDir))

	// Create a new cache instance with a different version.
	c2, err := NewCache(cacheDir, "v2.0.0")
	require.NoError(t, err)

	assert.False(t, c2.Validate("myrepo", "abc123"))
}

func TestEvict_RemovesEntry(t *testing.T) {
	cacheDir := t.TempDir()
	c, err := NewCache(cacheDir, "v1.0.0")
	require.NoError(t, err)

	indexDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(indexDir, "graph.bin"), []byte("data"), 0o644))

	require.NoError(t, c.Store("myrepo", "abc123", indexDir))
	assert.True(t, c.Check("myrepo", "abc123"))

	require.NoError(t, c.Evict("myrepo", "abc123"))
	assert.False(t, c.Check("myrepo", "abc123"))
}

func TestStore_OverwritesExistingEntry(t *testing.T) {
	cacheDir := t.TempDir()
	c, err := NewCache(cacheDir, "v1.0.0")
	require.NoError(t, err)

	// Store first version.
	indexDir1 := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(indexDir1, "graph.bin"), []byte("old-data"), 0o644))
	require.NoError(t, c.Store("myrepo", "abc123", indexDir1))

	// Store second version — should overwrite.
	indexDir2 := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(indexDir2, "graph.bin"), []byte("new-data"), 0o644))
	require.NoError(t, c.Store("myrepo", "abc123", indexDir2))

	path, err := c.Load("myrepo", "abc123")
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(path, "graph.bin"))
	require.NoError(t, err)
	assert.Equal(t, "new-data", string(data))
}

func TestVersionMismatch_StoreV1_ValidateWithV2(t *testing.T) {
	cacheDir := t.TempDir()

	// Store with v1.
	c1, err := NewCache(cacheDir, "v1.0.0")
	require.NoError(t, err)

	indexDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(indexDir, "graph.bin"), []byte("data"), 0o644))
	require.NoError(t, c1.Store("myrepo", "abc123", indexDir))

	// Create cache with v2 — validate should return false.
	c2, err := NewCache(cacheDir, "v2.0.0")
	require.NoError(t, err)

	assert.False(t, c2.Validate("myrepo", "abc123"))

	// Entry still exists (Check is version-agnostic).
	assert.True(t, c2.Check("myrepo", "abc123"))

	// Evict the stale entry.
	require.NoError(t, c2.Evict("myrepo", "abc123"))
	assert.False(t, c2.Check("myrepo", "abc123"))

	// Re-store with v2.
	require.NoError(t, c2.Store("myrepo", "abc123", indexDir))
	assert.True(t, c2.Validate("myrepo", "abc123"))
}
