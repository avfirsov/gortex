package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadOrCreateServerID_GeneratesWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "server.id")

	id, err := LoadOrCreateServerID(path)
	require.NoError(t, err)
	_, parseErr := uuid.Parse(id)
	assert.NoError(t, parseErr)

	// Persisted to disk.
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), id)
}

func TestLoadOrCreateServerID_ReusesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.id")

	first, err := LoadOrCreateServerID(path)
	require.NoError(t, err)

	second, err := LoadOrCreateServerID(path)
	require.NoError(t, err)

	assert.Equal(t, first, second, "server id should be stable across calls")
}

func TestLoadOrCreateServerID_ReplacesMalformedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.id")
	require.NoError(t, os.WriteFile(path, []byte("not a uuid"), 0o600))

	id, err := LoadOrCreateServerID(path)
	require.NoError(t, err)
	_, parseErr := uuid.Parse(id)
	assert.NoError(t, parseErr)
}
