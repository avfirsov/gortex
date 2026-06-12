package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponseBuffer_CaptureGetList(t *testing.T) {
	var b responseBuffer
	id1 := b.capture("read_file", "alpha")
	id2 := b.capture("smart_context", "beta")
	assert.Equal(t, "resp_1", id1)
	assert.Equal(t, "resp_2", id2)

	latest, ok := b.get("")
	require.True(t, ok)
	assert.Equal(t, "beta", latest.Text, "empty id resolves to the most recent capture")

	byLatest, ok := b.get("latest")
	require.True(t, ok)
	assert.Equal(t, "beta", byLatest.Text)

	first, ok := b.get("resp_1")
	require.True(t, ok)
	assert.Equal(t, "read_file", first.Tool)

	_, ok = b.get("resp_999")
	assert.False(t, ok, "unknown id must miss")

	list := b.list()
	require.Len(t, list, 2)
	assert.Equal(t, "resp_2", list[0].ID, "list is most-recent-first")
}

func TestResponseBuffer_Eviction(t *testing.T) {
	var b responseBuffer
	for i := 0; i < defaultResponseBufferCap+5; i++ {
		b.capture("t", "x")
	}
	assert.Len(t, b.list(), defaultResponseBufferCap, "the ring is bounded")
	_, ok := b.get("resp_1")
	assert.False(t, ok, "evicted entry must be gone")
	_, ok = b.get(fmt.Sprintf("resp_%d", defaultResponseBufferCap+5))
	assert.True(t, ok, "the newest entry must survive")
}

func TestResponseBuffer_EmptyGet(t *testing.T) {
	var b responseBuffer
	_, ok := b.get("")
	assert.False(t, ok, "an empty buffer resolves nothing")
}

func TestGrepBlock(t *testing.T) {
	lines := []string{"one", "two match", "three", "four", "five", "six match", "seven", "eight"}
	// Matches at rows 1 and 5 with context 1 → windows [0,2] and [4,6]
	// leave a gap at row 3, so a -- separator is emitted.
	block := grepBlock(lines, []int{1, 5}, 1, 1)
	for _, want := range []string{"1-one", "2:two match", "3-three", "--", "5-five", "6:six match", "7-seven"} {
		assert.Contains(t, block, want)
	}
}

func TestGrepBlock_MergesOverlappingWindows(t *testing.T) {
	lines := []string{"a", "m1", "b", "m2", "c"}
	// matches at rows 1 and 3 with context 1 → windows [0,2] and [2,4]
	// touch and must merge into one group.
	block := grepBlock(lines, []int{1, 3}, 1, 1)
	assert.NotContains(t, block, "--", "touching windows must merge")
	assert.Contains(t, block, "2:m1")
	assert.Contains(t, block, "4:m2")
}

func TestNormalizeForBuffer(t *testing.T) {
	indented := normalizeForBuffer(`{"a":1,"b":[1,2]}`)
	assert.Contains(t, indented, "\n", "compact JSON must be indented for line ops")

	assert.Equal(t, "GCX1 tool=x\nrow", normalizeForBuffer("GCX1 tool=x\nrow"),
		"non-JSON text passes through unchanged")
	assert.Equal(t, "{bad json", normalizeForBuffer("{bad json"),
		"invalid JSON passes through unchanged")
}

func TestCaptureResponse_Rules(t *testing.T) {
	srv, _ := setupCompressTestServer(t)
	ctx := context.Background()
	buf := srv.responseBufferFor(ctx)

	big := strings.Repeat("x", minCapturedResponseBytes+10)
	srv.captureResponse(ctx, "read_file", mcplib.NewToolResultText(big))
	require.Len(t, buf.list(), 1, "a large response must be captured")

	srv.captureResponse(ctx, "read_file", mcplib.NewToolResultText("tiny"))
	assert.Len(t, buf.list(), 1, "a small response must not be captured")

	srv.captureResponse(ctx, "ctx_grep", mcplib.NewToolResultText(big))
	assert.Len(t, buf.list(), 1, "a post-filter tool's own response must not be captured")

	srv.captureResponse(ctx, "read_file", mcplib.NewToolResultError(big))
	assert.Len(t, buf.list(), 1, "an error result must not be captured")
}

// seedBuffer captures a deterministic multi-line text: odd lines carry
// "keep", even lines "drop".
func seedBuffer(srv *Server, ctx context.Context, tool string, lineCount int) string {
	var b strings.Builder
	for i := 1; i <= lineCount; i++ {
		marker := "drop"
		if i%2 == 1 {
			marker = "keep"
		}
		fmt.Fprintf(&b, "line %d %s\n", i, marker)
	}
	return srv.responseBufferFor(ctx).capture(tool, strings.TrimRight(b.String(), "\n"))
}

func postFilterReq(args map[string]any) mcplib.CallToolRequest {
	req := mcplib.CallToolRequest{}
	req.Params.Arguments = args
	return req
}

func TestCtxStats_ListsAndDescribes(t *testing.T) {
	srv, _ := setupCompressTestServer(t)
	ctx := context.Background()
	id := seedBuffer(srv, ctx, "search_text", 30)

	all, err := srv.handleCtxStats(ctx, postFilterReq(map[string]any{}))
	require.NoError(t, err)
	m := extractTextResult(t, all)
	buffered, _ := m["buffered"].([]any)
	require.Len(t, buffered, 1)
	assert.Equal(t, float64(1), m["count"])
	e := buffered[0].(map[string]any)
	assert.Equal(t, "search_text", e["tool"])
	assert.Equal(t, float64(30), e["lines"])
	assert.Equal(t, id, e["response_id"])

	one, err := srv.handleCtxStats(ctx, postFilterReq(map[string]any{"response_id": id}))
	require.NoError(t, err)
	assert.Equal(t, float64(30), extractTextResult(t, one)["lines"])
}

func TestCtxGrep_MatchesAndBlock(t *testing.T) {
	srv, _ := setupCompressTestServer(t)
	ctx := context.Background()
	seedBuffer(srv, ctx, "search_text", 20) // 10 "keep" lines

	r, err := srv.handleCtxGrep(ctx, postFilterReq(map[string]any{"pattern": "keep"}))
	require.NoError(t, err)
	m := extractTextResult(t, r)
	assert.Equal(t, float64(10), m["match_count"])
	matches, _ := m["matches"].([]any)
	require.Len(t, matches, 10)
	first := matches[0].(map[string]any)
	assert.Equal(t, float64(1), first["line"])

	// A single match plus one line of context yields a grep-style block.
	rc, err := srv.handleCtxGrep(ctx, postFilterReq(map[string]any{
		"pattern": "line 7 ",
		"context": float64(1),
	}))
	require.NoError(t, err)
	block, _ := extractTextResult(t, rc)["block"].(string)
	assert.Contains(t, block, "6-line 6 drop")
	assert.Contains(t, block, "7:line 7 keep")
	assert.Contains(t, block, "8-line 8 drop")
}

func TestCtxGrep_InvalidPatternAndEmptyBuffer(t *testing.T) {
	srv, _ := setupCompressTestServer(t)
	ctx := context.Background()

	empty, err := srv.handleCtxGrep(ctx, postFilterReq(map[string]any{"pattern": "x"}))
	require.NoError(t, err)
	assert.True(t, empty.IsError, "grep on an empty buffer must error cleanly")

	seedBuffer(srv, ctx, "search_text", 5)
	bad, err := srv.handleCtxGrep(ctx, postFilterReq(map[string]any{"pattern": "(unclosed"}))
	require.NoError(t, err)
	assert.True(t, bad.IsError, "an invalid regex must error cleanly")
}

func TestCtxSliceAndHead(t *testing.T) {
	srv, _ := setupCompressTestServer(t)
	ctx := context.Background()
	seedBuffer(srv, ctx, "find_usages", 30)

	sl, err := srv.handleCtxSlice(ctx, postFilterReq(map[string]any{
		"from": float64(2), "to": float64(3),
	}))
	require.NoError(t, err)
	assert.Equal(t, "line 2 drop\nline 3 keep", extractTextResult(t, sl)["content"])

	hd, err := srv.handleHeadResults(ctx, postFilterReq(map[string]any{"lines": float64(2)}))
	require.NoError(t, err)
	hm := extractTextResult(t, hd)
	assert.Equal(t, "line 1 keep\nline 2 drop", hm["content"])
	assert.Equal(t, true, hm["truncated"])
}

func TestCtxPeek(t *testing.T) {
	srv, _ := setupCompressTestServer(t)
	ctx := context.Background()
	seedBuffer(srv, ctx, "get_repo_outline", 30)

	r, err := srv.handleCtxPeek(ctx, postFilterReq(map[string]any{"lines": float64(3)}))
	require.NoError(t, err)
	m := extractTextResult(t, r)
	assert.Equal(t, float64(30), m["total_lines"])
	assert.Equal(t, "line 1 keep\nline 2 drop\nline 3 keep", m["head"])
	assert.Equal(t, "line 28 drop\nline 29 keep\nline 30 drop", m["tail"])
	assert.Equal(t, float64(24), m["elided_lines"])
}

func TestPostFilter_EndToEndCapture(t *testing.T) {
	srv, dir := setupCompressTestServer(t)
	ctx := context.Background()

	// A file large enough that its read_file response clears the
	// capture threshold.
	var b strings.Builder
	b.WriteString("package post\n\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&b, "// helper %d does work\nfunc Helper%d() int { return %d }\n\n", i, i, i)
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "post.go"), []byte(b.String()), 0o644))

	// Route read_file through the real wrapToolHandler so the capture
	// hook fires exactly as it does in production.
	req := postFilterReq(map[string]any{"path": "post.go"})
	req.Params.Name = "read_file"
	_, err := srv.wrapToolHandler(srv.handleReadFile)(ctx, req)
	require.NoError(t, err)

	statsRes, err := srv.handleCtxStats(ctx, postFilterReq(map[string]any{}))
	require.NoError(t, err)
	stats := extractTextResult(t, statsRes)
	buffered, _ := stats["buffered"].([]any)
	require.NotEmpty(t, buffered, "wrapToolHandler must capture a large read_file response")
	assert.Equal(t, "read_file", buffered[0].(map[string]any)["tool"])

	grepRes, err := srv.handleCtxGrep(ctx, postFilterReq(map[string]any{"pattern": "Helper7"}))
	require.NoError(t, err)
	mc, _ := extractTextResult(t, grepRes)["match_count"].(float64)
	assert.Positive(t, mc, "the captured response must be greppable")
}
