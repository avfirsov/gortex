package mcp

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newHierarchyTestServer(t *testing.T) *Server {
	t.Helper()
	s := newMemoryTestServer(t)
	// Mount an in-memory global store. newMemoryManager with empty
	// args returns a no-disk manager — sufficient for tests.
	s.globalMemories = newMemoryManager("", "")
	return s
}

// edit_memory ---------------------------------------------------------

func TestEditMemory_RegexReplace(t *testing.T) {
	s := newHierarchyTestServer(t)
	res := callMemHandler(t, s.handleStoreMemory, map[string]any{
		"body": "uses Mutex everywhere",
	})
	id := unmarshalMemResult(t, res)["id"].(string)

	out := callMemHandler(t, s.handleEditMemory, map[string]any{
		"id":          id,
		"pattern":     `Mutex`,
		"replacement": "RWMutex",
	})
	body := unmarshalMemResult(t, out)["body"].(string)
	assert.Equal(t, "uses RWMutex everywhere", body)
}

func TestEditMemory_LiteralMode(t *testing.T) {
	s := newHierarchyTestServer(t)
	res := callMemHandler(t, s.handleStoreMemory, map[string]any{
		"body": "panics when .* is nil",
	})
	id := unmarshalMemResult(t, res)["id"].(string)

	out := callMemHandler(t, s.handleEditMemory, map[string]any{
		"id":          id,
		"pattern":     `.*`,
		"replacement": "the input",
		"mode":        "literal",
	})
	body := unmarshalMemResult(t, out)["body"].(string)
	assert.Equal(t, "panics when the input is nil", body)
}

func TestEditMemory_RegexGroups(t *testing.T) {
	s := newHierarchyTestServer(t)
	res := callMemHandler(t, s.handleStoreMemory, map[string]any{
		"body": "limit=100",
	})
	id := unmarshalMemResult(t, res)["id"].(string)

	out := callMemHandler(t, s.handleEditMemory, map[string]any{
		"id":          id,
		"pattern":     `limit=(\d+)`,
		"replacement": "limit=$1 (was here)",
	})
	body := unmarshalMemResult(t, out)["body"].(string)
	assert.Equal(t, "limit=100 (was here)", body)
}

func TestEditMemory_NoChangeIsIdempotent(t *testing.T) {
	s := newHierarchyTestServer(t)
	res := callMemHandler(t, s.handleStoreMemory, map[string]any{
		"body": "untouched",
	})
	id := unmarshalMemResult(t, res)["id"].(string)

	out := callMemHandler(t, s.handleEditMemory, map[string]any{
		"id":          id,
		"pattern":     `nonexistent`,
		"replacement": "x",
	})
	body := unmarshalMemResult(t, out)["body"].(string)
	assert.Equal(t, "untouched", body)
}

func TestEditMemory_BadRegex(t *testing.T) {
	s := newHierarchyTestServer(t)
	res := callMemHandler(t, s.handleStoreMemory, map[string]any{"body": "x"})
	id := unmarshalMemResult(t, res)["id"].(string)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"id":      id,
		"pattern": `(`, // unclosed group
	}
	resp, err := s.handleEditMemory(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, resp.IsError, "invalid regex should return error result")
}

func TestEditMemory_UnknownID(t *testing.T) {
	s := newHierarchyTestServer(t)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"id": "does-not-exist", "pattern": "x"}
	resp, err := s.handleEditMemory(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, resp.IsError)
}

func TestEditMemory_RejectsBadMode(t *testing.T) {
	s := newHierarchyTestServer(t)
	res := callMemHandler(t, s.handleStoreMemory, map[string]any{"body": "x"})
	id := unmarshalMemResult(t, res)["id"].(string)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"id": id, "pattern": "x", "mode": "nonsense"}
	resp, err := s.handleEditMemory(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, resp.IsError)
}

// global scope -------------------------------------------------------

func TestStoreMemory_GlobalScope(t *testing.T) {
	s := newHierarchyTestServer(t)
	out := callMemHandler(t, s.handleStoreMemory, map[string]any{
		"body":  "always use tokio::spawn_blocking for blocking calls",
		"kind":  "invariant",
		"scope": "global",
	})
	parsed := unmarshalMemResult(t, out)
	require.NotEmpty(t, parsed["id"])

	// Should be visible in global store.
	assert.Equal(t, 1, s.globalMemories.Count())
	// Should NOT be in the workspace store.
	assert.Equal(t, 0, s.memories.Count())
}

func TestQueryMemories_ScopeFilter(t *testing.T) {
	s := newHierarchyTestServer(t)
	_ = callMemHandler(t, s.handleStoreMemory, map[string]any{
		"body": "in workspace",
		"kind": "invariant",
	})
	_ = callMemHandler(t, s.handleStoreMemory, map[string]any{
		"body":  "in global",
		"kind":  "invariant",
		"scope": "global",
	})

	wOnly := unmarshalMemResult(t, callMemHandler(t, s.handleQueryMemories, map[string]any{
		"kind":  "invariant",
		"scope": "workspace",
	}))
	gOnly := unmarshalMemResult(t, callMemHandler(t, s.handleQueryMemories, map[string]any{
		"kind":  "invariant",
		"scope": "global",
	}))
	both := unmarshalMemResult(t, callMemHandler(t, s.handleQueryMemories, map[string]any{
		"kind":  "invariant",
		"scope": "both",
	}))

	assert.EqualValues(t, 1, wOnly["total"].(float64))
	assert.EqualValues(t, 1, gOnly["total"].(float64))
	assert.EqualValues(t, 2, both["total"].(float64))

	// Each row in scope=both should carry an explicit `scope` tag.
	mems, _ := both["memories"].([]any)
	scopes := map[string]int{}
	for _, m := range mems {
		scopes[m.(map[string]any)["scope"].(string)]++
	}
	assert.Equal(t, 1, scopes["workspace"])
	assert.Equal(t, 1, scopes["global"])
}

// rename_memory ------------------------------------------------------

func TestRenameMemory_WorkspaceToGlobal(t *testing.T) {
	s := newHierarchyTestServer(t)
	res := callMemHandler(t, s.handleStoreMemory, map[string]any{
		"body": "promote me",
	})
	id := unmarshalMemResult(t, res)["id"].(string)

	out := callMemHandler(t, s.handleRenameMemory, map[string]any{
		"id":       id,
		"to_scope": "global",
	})
	moved := unmarshalMemResult(t, out)
	assert.Equal(t, "global", moved["scope"])
	assert.Equal(t, id, moved["previous_id"], "rename surfaces the previous id")

	// Source store no longer has it; destination does.
	assert.Equal(t, 0, s.memories.Count())
	assert.Equal(t, 1, s.globalMemories.Count())
}

func TestRenameMemory_GlobalToWorkspace(t *testing.T) {
	s := newHierarchyTestServer(t)
	res := callMemHandler(t, s.handleStoreMemory, map[string]any{
		"body":  "demote me",
		"scope": "global",
	})
	id := unmarshalMemResult(t, res)["id"].(string)

	out := callMemHandler(t, s.handleRenameMemory, map[string]any{
		"id":         id,
		"from_scope": "global",
		"to_scope":   "workspace",
	})
	moved := unmarshalMemResult(t, out)
	assert.Equal(t, "workspace", moved["scope"])
	assert.Equal(t, 0, s.globalMemories.Count())
	assert.Equal(t, 1, s.memories.Count())
}

func TestRenameMemory_RejectsSameScope(t *testing.T) {
	s := newHierarchyTestServer(t)
	res := callMemHandler(t, s.handleStoreMemory, map[string]any{"body": "x"})
	id := unmarshalMemResult(t, res)["id"].(string)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"id": id, "to_scope": "workspace"}
	resp, err := s.handleRenameMemory(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, resp.IsError)
}

func TestRenameMemory_UnknownScope(t *testing.T) {
	s := newHierarchyTestServer(t)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"id": "anything", "to_scope": "moon"}
	resp, err := s.handleRenameMemory(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, resp.IsError)
}

func TestRenameMemory_UnknownID(t *testing.T) {
	s := newHierarchyTestServer(t)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"id": "does-not-exist", "to_scope": "global"}
	resp, err := s.handleRenameMemory(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, resp.IsError)
}

func TestResolveMemoryStore(t *testing.T) {
	s := newHierarchyTestServer(t)
	assert.Equal(t, s.memories, s.resolveMemoryStore(""))
	assert.Equal(t, s.memories, s.resolveMemoryStore("workspace"))
	assert.Equal(t, s.globalMemories, s.resolveMemoryStore("global"))
	// Unknown values fall through to workspace.
	assert.Equal(t, s.memories, s.resolveMemoryStore("garbage"))
}

func TestResolveMemoryStores(t *testing.T) {
	s := newHierarchyTestServer(t)
	assert.Len(t, s.resolveMemoryStores(""), 1)
	assert.Len(t, s.resolveMemoryStores("workspace"), 1)
	assert.Len(t, s.resolveMemoryStores("global"), 1)
	assert.Len(t, s.resolveMemoryStores("both"), 2)
}

func TestDefaultIfEmpty(t *testing.T) {
	assert.Equal(t, "fallback", defaultIfEmpty("", "fallback"))
	assert.Equal(t, "x", defaultIfEmpty("x", "fallback"))
}
