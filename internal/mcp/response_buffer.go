package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// defaultResponseBufferCap is how many recent tool responses a session
// keeps available for post-filter re-cutting.
const (
	defaultResponseBufferCap   = 8
	defaultResponseBufferBytes = 8 << 20
)

// minCapturedResponseBytes is the capture floor: responses smaller
// than this are cheap to re-fetch, so they never displace a ring slot.
const minCapturedResponseBytes = 1024

// postFilterTools never have their own responses captured — re-cutting
// a re-cut would only churn the ring.
var postFilterTools = map[string]bool{
	"ctx_stats":    true,
	"ctx_peek":     true,
	"ctx_slice":    true,
	"ctx_grep":     true,
	"grep_results": true,
	"head_results": true,
}

// bufferedResponse is one captured tool response held for re-cutting.
type bufferedResponse struct {
	ID         string
	Tool       string
	Text       string
	CapturedAt time.Time
}

// responseBuffer is a per-session ring of recent large tool responses.
// It backs the post-filter tools (ctx_grep, ctx_slice, …) so an agent
// can re-cut a prior result without re-issuing the original query.
type responseBuffer struct {
	mu         sync.Mutex
	entries    []bufferedResponse
	totalBytes int
	seq        int
}

// capture stores a response and returns its handle ID. The oldest entries are
// evicted, and their strings released, once either the count or byte budget is
// full. A single response larger than the whole budget is not retained.
func (b *responseBuffer) capture(tool, text string) string {
	if len(text) > defaultResponseBufferBytes {
		return ""
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	id := fmt.Sprintf("resp_%d", b.seq)
	b.entries = append(b.entries, bufferedResponse{
		ID:         id,
		Tool:       tool,
		Text:       text,
		CapturedAt: time.Now(),
	})
	b.totalBytes += len(text)
	for len(b.entries) > defaultResponseBufferCap || b.totalBytes > defaultResponseBufferBytes {
		b.totalBytes -= len(b.entries[0].Text)
		b.entries[0] = bufferedResponse{}
		b.entries = b.entries[1:]
	}
	return id
}

// get resolves a handle. An empty or "latest" id returns the most
// recent capture; the bool is false when the buffer is empty or the id
// is unknown.
func (b *responseBuffer) get(id string) (bufferedResponse, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.entries) == 0 {
		return bufferedResponse{}, false
	}
	if id == "" || id == "latest" {
		return b.entries[len(b.entries)-1], true
	}
	for i := len(b.entries) - 1; i >= 0; i-- {
		if b.entries[i].ID == id {
			return b.entries[i], true
		}
	}
	return bufferedResponse{}, false
}

// list returns every buffered response, most recent first.
func (b *responseBuffer) list() []bufferedResponse {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]bufferedResponse, 0, len(b.entries))
	for i := len(b.entries) - 1; i >= 0; i-- {
		out = append(out, b.entries[i])
	}
	return out
}

// responseBufferFor returns the calling session's response buffer,
// allocating it on first use.
func (s *Server) responseBufferFor(ctx context.Context) *responseBuffer {
	sess := s.sessionFor(ctx)
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.responses == nil {
		sess.responses = &responseBuffer{}
	}
	return sess.responses
}

// captureResponse stores a large, successful, non-post-filter tool
// response in the session ring so it can be re-cut later. Called from
// wrapToolHandler for every tool call.
func (s *Server) captureResponse(ctx context.Context, tool string, res *mcp.CallToolResult) {
	if res == nil || res.IsError || postFilterTools[tool] {
		return
	}
	raw := toolResultText(res)
	if len(raw) < minCapturedResponseBytes || len(raw) > defaultResponseBufferBytes {
		return
	}
	text := normalizeForBuffer(raw)
	if len(text) > defaultResponseBufferBytes {
		return
	}
	s.responseBufferFor(ctx).capture(tool, text)
}

// normalizeForBuffer renders a compact-JSON response as indented text
// so the post-filter tools' line-based slicing and grep land on
// meaningful lines. Non-JSON text (GCX, TOON) is already
// line-structured and passes through unchanged.
func normalizeForBuffer(text string) string {
	t := strings.TrimSpace(text)
	if t == "" || (t[0] != '{' && t[0] != '[') {
		return text
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(text), "", "  "); err != nil {
		return text
	}
	return buf.String()
}

// toolResultText extracts the concatenated text content of a tool
// result. The common single-block case is returned without a copy.
func toolResultText(res *mcp.CallToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	if len(res.Content) == 1 {
		if tc, ok := res.Content[0].(mcp.TextContent); ok {
			return tc.Text
		}
		return ""
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
