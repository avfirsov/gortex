package mcp

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

// newAnnotateTestServer builds a minimal Server over a small in-memory
// graph with the annotate tool registered, reusing the same field set
// the notes/handler tests use. The two seed nodes are the annotation
// targets for the handler tests below.
func newAnnotateTestServer(t *testing.T) *Server {
	t.Helper()
	g := graph.New()
	g.AddNode(&graph.Node{ID: "pkg/foo.go::Bar", Name: "Bar", Kind: graph.KindFunction, FilePath: "pkg/foo.go", StartLine: 1, EndLine: 9})
	g.AddNode(&graph.Node{ID: "pkg/foo.go::Baz", Name: "Baz", Kind: graph.KindMethod, FilePath: "pkg/foo.go", StartLine: 11, EndLine: 20})

	s := &Server{
		graph:      g,
		session:    newSessionState(),
		tokenStats: &tokenStats{},
		symHistory: &symbolHistory{entries: make(map[string][]SymbolModification)},
		sessions:   newSessionMap(),
		toolScopes: newScopeRegistry(),
	}
	return s
}

// TestHandleAnnotateNodes_RoundTripAndIdempotent is the core handler
// test: annotate two nodes inline, assert the JSON summary, assert
// GetNode reflects the merged namespaced keys with structural fields
// untouched, then re-run the identical batch and assert it reports
// everything unchanged (idempotent).
func TestHandleAnnotateNodes_RoundTripAndIdempotent(t *testing.T) {
	s := newAnnotateTestServer(t)

	annotations := `[
		{"id":"pkg/foo.go::Bar","meta":{"summary":"parses the bar","tags":["parser","io"],"complexity":0.7,"domain":"ingest"}},
		{"id":"pkg/foo.go::Baz","meta":{"summary":"formats the baz"}},
		{"id":"pkg/foo.go::Ghost","meta":{"summary":"no such node"}}
	]`

	res := callHandler(t, s.handleAnnotateNodes, map[string]any{"annotations": annotations})
	out := unmarshalResult(t, res)
	t.Logf("[annotate#1] summary=%v", out)

	assert.Equal(t, 2.0, out["annotated"], "two real nodes annotated")
	assert.Equal(t, 0.0, out["unchanged"])
	assert.Equal(t, 0.0, out["edges_added"])
	notFound, _ := out["not_found"].([]any)
	require.Len(t, notFound, 1)
	assert.Equal(t, "pkg/foo.go::Ghost", notFound[0])

	// Round-trip: GetNode reflects the merged keys under the default
	// "ext_" namespace.
	bar := s.graph.GetNode("pkg/foo.go::Bar")
	require.NotNil(t, bar)
	assert.Equal(t, "parses the bar", bar.Meta["ext_summary"])
	assert.Equal(t, "ingest", bar.Meta["ext_domain"])
	assert.Equal(t, 0.7, bar.Meta["ext_complexity"])
	assert.Equal(t, []any{"parser", "io"}, bar.Meta["ext_tags"])
	// Structural fields untouched (MUST NOT).
	assert.Equal(t, "Bar", bar.Name)
	assert.Equal(t, graph.KindFunction, bar.Kind)
	assert.Equal(t, 1, bar.StartLine)
	assert.Equal(t, 9, bar.EndLine)

	// Idempotent re-run: same batch -> the two real nodes are unchanged.
	res2 := callHandler(t, s.handleAnnotateNodes, map[string]any{"annotations": annotations})
	out2 := unmarshalResult(t, res2)
	t.Logf("[annotate#2 idempotent] summary=%v", out2)
	assert.Equal(t, 0.0, out2["annotated"], "re-run annotates nothing new")
	assert.Equal(t, 2.0, out2["unchanged"], "both real nodes report unchanged")
}

// TestHandleAnnotateNodes_Namespace proves the namespace argument drives
// the key prefix, and that a key already carrying the prefix is not
// double-prefixed.
func TestHandleAnnotateNodes_Namespace(t *testing.T) {
	s := newAnnotateTestServer(t)

	res := callHandler(t, s.handleAnnotateNodes, map[string]any{
		"namespace":   "ua",
		"annotations": `[{"id":"pkg/foo.go::Bar","meta":{"summary":"x","ua_domain":"core"}}]`,
	})
	out := unmarshalResult(t, res)
	assert.Equal(t, 1.0, out["annotated"])

	bar := s.graph.GetNode("pkg/foo.go::Bar")
	require.NotNil(t, bar)
	assert.Equal(t, "x", bar.Meta["ua_summary"], "bare key gets the namespace prefix")
	assert.Equal(t, "core", bar.Meta["ua_domain"], "already-prefixed key is not double-prefixed")
	_, doubled := bar.Meta["ua_ua_domain"]
	assert.False(t, doubled, "must not double-prefix")
}

// TestHandleAnnotateNodes_AddRelatedIdempotentEdge proves the optional
// add_related pairs create an idempotent semantically_related edge with
// the expected origin/confidence/similarity meta.
func TestHandleAnnotateNodes_AddRelatedIdempotentEdge(t *testing.T) {
	s := newAnnotateTestServer(t)

	args := map[string]any{
		"annotations": `[{"id":"pkg/foo.go::Bar","meta":{"summary":"x"}}]`,
		"add_related": `[["pkg/foo.go::Bar","pkg/foo.go::Baz",0.83]]`,
	}
	res := callHandler(t, s.handleAnnotateNodes, args)
	out := unmarshalResult(t, res)
	t.Logf("[related#1] summary=%v", out)
	assert.Equal(t, 1.0, out["edges_added"])

	// The edge exists with the expected shape.
	outEdges := s.graph.GetOutEdges("pkg/foo.go::Bar")
	var rel *graph.Edge
	for _, e := range outEdges {
		if e.Kind == graph.EdgeSemanticallyRelated && e.To == "pkg/foo.go::Baz" {
			rel = e
			break
		}
	}
	require.NotNil(t, rel, "semantically_related edge should be present")
	assert.Equal(t, "ext_annotated", rel.Origin)
	assert.InDelta(t, 0.83, rel.Confidence, 1e-9)
	assert.InDelta(t, 0.83, rel.Meta["similarity"].(float64), 1e-9)

	// Idempotent: re-adding the same pair does not duplicate the edge.
	callHandler(t, s.handleAnnotateNodes, args)
	count := 0
	for _, e := range s.graph.GetOutEdges("pkg/foo.go::Bar") {
		if e.Kind == graph.EdgeSemanticallyRelated && e.To == "pkg/foo.go::Baz" {
			count++
		}
	}
	assert.Equal(t, 1, count, "AddEdge dedup keeps a single semantically_related edge")
}

// TestHandleAnnotateNodes_DefaultScore proves a pair without an explicit
// score defaults to 0.5.
func TestHandleAnnotateNodes_DefaultScore(t *testing.T) {
	s := newAnnotateTestServer(t)
	callHandler(t, s.handleAnnotateNodes, map[string]any{
		"annotations": `[{"id":"pkg/foo.go::Bar","meta":{"summary":"x"}}]`,
		"add_related": `[["pkg/foo.go::Bar","pkg/foo.go::Baz"]]`,
	})
	for _, e := range s.graph.GetOutEdges("pkg/foo.go::Bar") {
		if e.Kind == graph.EdgeSemanticallyRelated {
			assert.InDelta(t, 0.5, e.Confidence, 1e-9, "missing score defaults to 0.5")
		}
	}
}

// TestHandleAnnotateNodes_BadInput proves malformed JSON and a missing
// required arg surface a clean error result (not a panic).
func TestHandleAnnotateNodes_BadInput(t *testing.T) {
	s := newAnnotateTestServer(t)

	missing, err := s.handleAnnotateNodes(context.Background(), reqWith(map[string]any{}))
	require.NoError(t, err)
	assert.True(t, missing.IsError, "missing 'annotations' is an error result")

	bad, err := s.handleAnnotateNodes(context.Background(), reqWith(map[string]any{"annotations": "{not json"}))
	require.NoError(t, err)
	assert.True(t, bad.IsError, "malformed annotations JSON is an error result")
}

// TestAnnotate_RegisteredOnNewServer guards against accidental removal
// of the registerAnnotateTools() call: the tool must be listed by a
// Server built through the real NewServer (registered + callable, and
// thus HTTP-exposed via the shared registry).
func TestAnnotate_RegisteredOnNewServer(t *testing.T) {
	srv, _ := setupTestServer(t)
	require.Contains(t, srv.mcpServer.ListTools(), "annotate_nodes",
		"annotate_nodes must be registered on a NewServer-built server")
}

// reqWith builds a CallToolRequest carrying the given arguments.
func reqWith(args map[string]any) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	return req
}
