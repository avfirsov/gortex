package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/analysis"
	"github.com/zzet/gortex/internal/graph"
)

// newSurprisingTestServer seeds a small synthetic graph where each
// signal can be triggered in isolation. Each test asks for one edge's
// breakdown and asserts that the matching signal fired.
func newSurprisingTestServer(t *testing.T) *Server {
	t.Helper()
	g := graph.New()
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

func callSurprisingHandler(t *testing.T, s *Server, args map[string]any) map[string]any {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := s.handleGetSurprisingConnections(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.False(t, res.IsError, "handler error: %+v", res.Content)
	tc, ok := res.Content[0].(mcp.TextContent)
	require.True(t, ok)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &m))
	return m
}

// findEdgeRow returns the row whose from/to match. Tests narrow the
// graph enough that each edge has a unique pair.
func findEdgeRow(out map[string]any, from, to string) map[string]any {
	conns, _ := out["connections"].([]any)
	for _, c := range conns {
		m := c.(map[string]any)
		if m["from"] == from && m["to"] == to {
			return m
		}
	}
	return nil
}

func TestSurprising_CrossCommunityFires(t *testing.T) {
	s := newSurprisingTestServer(t)
	s.graph.AddNode(&graph.Node{ID: "a", Name: "a", Kind: graph.KindFunction, FilePath: "p/a.go", Language: "go"})
	s.graph.AddNode(&graph.Node{ID: "b", Name: "b", Kind: graph.KindFunction, FilePath: "p/b.go", Language: "go"})
	s.graph.AddEdge(&graph.Edge{From: "a", To: "b", Kind: graph.EdgeCalls})

	// Two different communities so cross_community fires.
	s.analysisMu.Lock()
	s.communities = &analysis.CommunityResult{
		NodeToComm: map[string]string{"a": "c1", "b": "c2"},
	}
	s.analysisMu.Unlock()

	out := callSurprisingHandler(t, s, map[string]any{"min_score": 0.1})
	row := findEdgeRow(out, "a", "b")
	require.NotNil(t, row, "a→b should surface when cross_community fires")
	signals := row["signals"].(map[string]any)
	assert.InDelta(t, 0.30, signals["cross_community"].(float64), 1e-9)
}

func TestSurprising_CrossLanguageFires(t *testing.T) {
	s := newSurprisingTestServer(t)
	s.graph.AddNode(&graph.Node{ID: "a", Name: "a", Kind: graph.KindFunction, FilePath: "p/a.go", Language: "go"})
	s.graph.AddNode(&graph.Node{ID: "b", Name: "b", Kind: graph.KindFunction, FilePath: "p/b.ts", Language: "typescript"})
	s.graph.AddEdge(&graph.Edge{From: "a", To: "b", Kind: graph.EdgeCalls})

	out := callSurprisingHandler(t, s, map[string]any{"min_score": 0.1})
	row := findEdgeRow(out, "a", "b")
	require.NotNil(t, row)
	signals := row["signals"].(map[string]any)
	assert.InDelta(t, 0.20, signals["cross_language"].(float64), 1e-9)
}

func TestSurprising_PeripheralToHubFires(t *testing.T) {
	s := newSurprisingTestServer(t)
	// Hub h with 6 incoming callers (above default hub_threshold=5).
	s.graph.AddNode(&graph.Node{ID: "h", Name: "h", Kind: graph.KindFunction, FilePath: "p/h.go", Language: "go"})
	for i := range 6 {
		id := "c" + string(rune('A'+i))
		s.graph.AddNode(&graph.Node{ID: id, Name: id, Kind: graph.KindFunction, FilePath: "p/c.go", Language: "go"})
		s.graph.AddEdge(&graph.Edge{From: id, To: "h", Kind: graph.EdgeCalls})
	}
	// Peripheral node p — zero in-edges of its own — connects to h.
	s.graph.AddNode(&graph.Node{ID: "p", Name: "p", Kind: graph.KindFunction, FilePath: "p/p.go", Language: "go"})
	s.graph.AddEdge(&graph.Edge{From: "p", To: "h", Kind: graph.EdgeCalls})

	out := callSurprisingHandler(t, s, map[string]any{"min_score": 0.1})
	row := findEdgeRow(out, "p", "h")
	require.NotNil(t, row)
	signals := row["signals"].(map[string]any)
	assert.InDelta(t, 0.20, signals["peripheral_to_hub"].(float64), 1e-9, "peripheral→hub edge fires the signal")
}

func TestSurprising_CrossTestFires(t *testing.T) {
	s := newSurprisingTestServer(t)
	s.graph.AddNode(&graph.Node{ID: "prod", Name: "Prod", Kind: graph.KindFunction, FilePath: "p/prod.go", Language: "go"})
	s.graph.AddNode(&graph.Node{ID: "test", Name: "Test", Kind: graph.KindFunction, FilePath: "p/prod_test.go", Language: "go"})
	s.graph.AddEdge(&graph.Edge{From: "test", To: "prod", Kind: graph.EdgeCalls})

	out := callSurprisingHandler(t, s, map[string]any{"min_score": 0.1})
	row := findEdgeRow(out, "test", "prod")
	require.NotNil(t, row)
	signals := row["signals"].(map[string]any)
	assert.InDelta(t, 0.15, signals["cross_test"].(float64), 1e-9)
}

func TestSurprising_UnusualKindFires(t *testing.T) {
	s := newSurprisingTestServer(t)
	// 20 EdgeCalls + 1 EdgeImports = imports is ~5%, on the boundary.
	// Use rare_kind_pct=10 so imports counts as rare; calls (95%) does not.
	for i := range 20 {
		from := "f" + string(rune('A'+i%26))
		to := "g" + string(rune('A'+i%26))
		s.graph.AddNode(&graph.Node{ID: from + string(rune('0'+i)), Name: from, Kind: graph.KindFunction, FilePath: "p/x.go", Language: "go"})
		s.graph.AddNode(&graph.Node{ID: to + string(rune('0'+i)), Name: to, Kind: graph.KindFunction, FilePath: "p/y.go", Language: "go"})
		s.graph.AddEdge(&graph.Edge{From: from + string(rune('0'+i)), To: to + string(rune('0'+i)), Kind: graph.EdgeCalls})
	}
	s.graph.AddNode(&graph.Node{ID: "uA", Name: "uA", Kind: graph.KindFunction, FilePath: "p/u.go", Language: "go"})
	s.graph.AddNode(&graph.Node{ID: "uB", Name: "uB", Kind: graph.KindFunction, FilePath: "p/u.go", Language: "go"})
	s.graph.AddEdge(&graph.Edge{From: "uA", To: "uB", Kind: graph.EdgeImports})

	out := callSurprisingHandler(t, s, map[string]any{"min_score": 0.1, "rare_kind_pct": 10})
	row := findEdgeRow(out, "uA", "uB")
	require.NotNil(t, row, "rare-kind edge should surface")
	signals := row["signals"].(map[string]any)
	assert.InDelta(t, 0.15, signals["unusual_kind"].(float64), 1e-9)
}

func TestSurprising_MinScoreFilters(t *testing.T) {
	s := newSurprisingTestServer(t)
	// Edge with no firing signals; same community, same language, low fan-in, no cross-test, common kind.
	s.graph.AddNode(&graph.Node{ID: "a", Name: "a", Kind: graph.KindFunction, FilePath: "p/a.go", Language: "go"})
	s.graph.AddNode(&graph.Node{ID: "b", Name: "b", Kind: graph.KindFunction, FilePath: "p/b.go", Language: "go"})
	s.graph.AddEdge(&graph.Edge{From: "a", To: "b", Kind: graph.EdgeCalls})
	s.analysisMu.Lock()
	s.communities = &analysis.CommunityResult{NodeToComm: map[string]string{"a": "c1", "b": "c1"}}
	s.analysisMu.Unlock()

	out := callSurprisingHandler(t, s, map[string]any{"min_score": 0.3})
	conns, _ := out["connections"].([]any)
	assert.Empty(t, conns, "no firing signals → no rows")
}

func TestSurprising_LimitTruncates(t *testing.T) {
	s := newSurprisingTestServer(t)
	// 30 cross-language edges; cap to 5.
	for i := range 30 {
		fromID := "go" + string(rune('A'+i%26)) + string(rune('0'+i))
		toID := "ts" + string(rune('A'+i%26)) + string(rune('0'+i))
		s.graph.AddNode(&graph.Node{ID: fromID, Name: fromID, Kind: graph.KindFunction, FilePath: "p/a.go", Language: "go"})
		s.graph.AddNode(&graph.Node{ID: toID, Name: toID, Kind: graph.KindFunction, FilePath: "p/a.ts", Language: "typescript"})
		s.graph.AddEdge(&graph.Edge{From: fromID, To: toID, Kind: graph.EdgeCalls})
	}

	out := callSurprisingHandler(t, s, map[string]any{"limit": 5, "min_score": 0.1})
	conns, _ := out["connections"].([]any)
	assert.Len(t, conns, 5)
	assert.Equal(t, true, out["truncated"])
}

func TestSurprising_CompositeScoreStacks(t *testing.T) {
	s := newSurprisingTestServer(t)
	// Cross-community + cross-language + cross-test on the same edge.
	s.graph.AddNode(&graph.Node{ID: "a", Name: "a", Kind: graph.KindFunction, FilePath: "p/a.go", Language: "go"})
	s.graph.AddNode(&graph.Node{ID: "b", Name: "b", Kind: graph.KindFunction, FilePath: "p/b.test.ts", Language: "typescript"})
	s.graph.AddEdge(&graph.Edge{From: "a", To: "b", Kind: graph.EdgeCalls})
	s.analysisMu.Lock()
	s.communities = &analysis.CommunityResult{NodeToComm: map[string]string{"a": "c1", "b": "c2"}}
	s.analysisMu.Unlock()

	out := callSurprisingHandler(t, s, map[string]any{"min_score": 0.1})
	row := findEdgeRow(out, "a", "b")
	require.NotNil(t, row)
	// 0.30 (community) + 0.20 (language) + 0.15 (test) = 0.65
	assert.InDelta(t, 0.65, row["score"].(float64), 1e-6)
	reasons, _ := row["reasons"].([]any)
	assert.Len(t, reasons, 3, "three signals contributed")
}

func TestSurprising_PathPrefixFilter(t *testing.T) {
	s := newSurprisingTestServer(t)
	s.graph.AddNode(&graph.Node{ID: "scoped", Name: "scoped", Kind: graph.KindFunction, FilePath: "internal/x.go", Language: "go"})
	s.graph.AddNode(&graph.Node{ID: "scoped2", Name: "scoped2", Kind: graph.KindFunction, FilePath: "internal/y.ts", Language: "typescript"})
	s.graph.AddEdge(&graph.Edge{From: "scoped", To: "scoped2", Kind: graph.EdgeCalls})

	s.graph.AddNode(&graph.Node{ID: "outsideA", Name: "outsideA", Kind: graph.KindFunction, FilePath: "vendor/x.go", Language: "go"})
	s.graph.AddNode(&graph.Node{ID: "outsideB", Name: "outsideB", Kind: graph.KindFunction, FilePath: "vendor/y.ts", Language: "typescript"})
	s.graph.AddEdge(&graph.Edge{From: "outsideA", To: "outsideB", Kind: graph.EdgeCalls})

	out := callSurprisingHandler(t, s, map[string]any{"min_score": 0.1, "path_prefix": "internal/"})
	conns, _ := out["connections"].([]any)
	require.Len(t, conns, 1)
	row := conns[0].(map[string]any)
	assert.Equal(t, "scoped", row["from"])
}

func TestSurprising_WeightsExposed(t *testing.T) {
	s := newSurprisingTestServer(t)
	out := callSurprisingHandler(t, s, map[string]any{})
	w := out["signal_weights"].(map[string]any)
	assert.InDelta(t, 0.30, w["cross_community"].(float64), 1e-9)
	assert.InDelta(t, 0.20, w["cross_language"].(float64), 1e-9)
	assert.InDelta(t, 0.20, w["peripheral_to_hub"].(float64), 1e-9)
	assert.InDelta(t, 0.15, w["cross_test"].(float64), 1e-9)
	assert.InDelta(t, 0.15, w["unusual_kind"].(float64), 1e-9)
}

func TestIsTestPath(t *testing.T) {
	cases := map[string]bool{
		"foo_test.go":          true,
		"foo.test.ts":          true,
		"foo.spec.js":          true,
		"__tests__/foo.ts":     true,
		"test_foo.py":          true,
		"foo.go":               false,
		"internal/foo.go":      false,
		"foo/internal/test.ts": false, // requires test_ prefix, not test as a path segment
	}
	for p, want := range cases {
		assert.Equal(t, want, isTestPath(p), "isTestPath(%q)", p)
	}
}
