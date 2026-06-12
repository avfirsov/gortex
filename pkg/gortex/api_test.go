package gortex

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEngine_IndexAndQuery(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main

func Hello() {}
func World() { Hello() }
`), 0o644))

	eng := New(WithWorkers(1))
	result, err := eng.Index(dir)
	require.NoError(t, err)

	assert.Equal(t, 1, result.FileCount)
	assert.Greater(t, result.NodeCount, 0)

	// FindSymbols.
	nodes := eng.FindSymbols("Hello")
	require.Len(t, nodes, 1)
	assert.Equal(t, "Hello", nodes[0].Name)

	// Stats.
	stats := eng.Stats()
	assert.Greater(t, stats.TotalNodes, 0)
	assert.Equal(t, stats.TotalNodes, result.NodeCount)
}
