package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileMutationsReindexSynchronouslyAcrossConsecutiveEdits(t *testing.T) {
	srv, _ := setupTestServer(t)

	write := callTool(t, srv, "write_file", map[string]any{
		"path":    "fresh.go",
		"content": "package fresh\n\nfunc First() {}\n",
	})
	require.False(t, write.IsError)
	writeResp := decodeFileOpsResult(t, write)
	assert.Equal(t, true, writeResp["reindexed"])
	assert.NotContains(t, writeResp, "reindex_pending")

	path, ok := writeResp["path"].(string)
	require.True(t, ok)
	assertSymbols := func(present string, absent ...string) {
		t.Helper()
		names := make(map[string]bool)
		for _, node := range srv.graph.GetFileNodes(path) {
			names[node.Name] = true
		}
		assert.True(t, names[present], "expected %q in the graph immediately after mutation", present)
		for _, name := range absent {
			assert.False(t, names[name], "did not expect stale symbol %q after mutation", name)
		}
	}
	assertSymbols("First")

	for _, edit := range []struct {
		oldName string
		newName string
	}{
		{oldName: "First", newName: "Second"},
		{oldName: "Second", newName: "Third"},
	} {
		result := callTool(t, srv, "edit_file", map[string]any{
			"path":                 path,
			"old_string":           edit.oldName,
			"new_string":           edit.newName,
			"expected_occurrences": 1,
		})
		require.False(t, result.IsError)
		resp := decodeFileOpsResult(t, result)
		assert.Equal(t, true, resp["reindexed"])
		assert.NotContains(t, resp, "reindex_pending")
		assertSymbols(edit.newName, edit.oldName)
	}
}
