package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

func newReplayTestServer(t *testing.T) *Server {
	t.Helper()
	g := graph.New()

	// anchor — the place the bug surfaced.
	g.AddNode(&graph.Node{
		ID: "p/handler.go::Handle", Name: "Handle", Kind: graph.KindFunction,
		FilePath: "p/handler.go", StartLine: 5,
		Meta: map[string]any{
			"last_commit_at": time.Now().Add(-2 * 24 * time.Hour).UTC().Format(time.RFC3339),
			"last_author":    "alice@example.com",
			"coverage_pct":   30.0,
		},
	})

	// direct caller — depth 1.
	g.AddNode(&graph.Node{
		ID: "p/router.go::Route", Name: "Route", Kind: graph.KindFunction,
		FilePath: "p/router.go", StartLine: 10,
		Meta: map[string]any{
			"last_commit_at": time.Now().Add(-5 * 24 * time.Hour).UTC().Format(time.RFC3339),
			"last_author":    "bob@example.com",
			"coverage_pct":   85.0,
		},
	})
	g.AddEdge(&graph.Edge{From: "p/router.go::Route", To: "p/handler.go::Handle", Kind: graph.EdgeCalls})

	// indirect caller — depth 2.
	g.AddNode(&graph.Node{
		ID: "p/main.go::main", Name: "main", Kind: graph.KindFunction,
		FilePath: "p/main.go",
		Meta: map[string]any{
			"last_commit_at": time.Now().Add(-100 * 24 * time.Hour).UTC().Format(time.RFC3339),
			"last_author":    "carol@example.com",
		},
	})
	g.AddEdge(&graph.Edge{From: "p/main.go::main", To: "p/router.go::Route", Kind: graph.EdgeCalls})

	// unrelated node — should NOT appear in the radius.
	g.AddNode(&graph.Node{ID: "p/unrelated.go::Z", Name: "Z", Kind: graph.KindFunction, FilePath: "p/unrelated.go"})

	s := &Server{
		graph:      g,
		session:    newSessionState(),
		tokenStats: &tokenStats{},
		symHistory: &symbolHistory{entries: make(map[string][]SymbolModification)},
		sessions:   newSessionMap(),
		toolScopes: newScopeRegistry(),
	}
	s.memories = newMemoryManager("", "")
	return s
}

func callReplayHandler(t *testing.T, s *Server, args map[string]any) map[string]any {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := s.handleReplayEpisode(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, res)
	if res.IsError {
		return map[string]any{"is_error": true, "content": res.Content}
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	require.True(t, ok)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &m))
	return m
}

func TestReplayEpisode_HappyPath(t *testing.T) {
	s := newReplayTestServer(t)
	out := callReplayHandler(t, s, map[string]any{
		"anchor_symbol": "p/handler.go::Handle",
		"depth":         3,
	})

	anchor := out["anchor"].(map[string]any)
	assert.Equal(t, "p/handler.go::Handle", anchor["id"])

	// Radius size includes anchor + 2 callers.
	assert.EqualValues(t, 3, out["radius_size"].(float64))

	// Callers include router and main with correct depths.
	callers, _ := out["callers"].([]any)
	require.Len(t, callers, 2)
	depthByID := map[string]int{}
	for _, c := range callers {
		m := c.(map[string]any)
		depthByID[m["id"].(string)] = int(m["depth"].(float64))
	}
	assert.Equal(t, 1, depthByID["p/router.go::Route"])
	assert.Equal(t, 2, depthByID["p/main.go::main"])
}

func TestReplayEpisode_WindowDaysFiltersOldEdits(t *testing.T) {
	s := newReplayTestServer(t)
	// main.go is 100 days old; window of 10 days should exclude it.
	out := callReplayHandler(t, s, map[string]any{
		"anchor_symbol": "p/handler.go::Handle",
		"window_days":   10,
		"depth":         3,
	})

	timeline, _ := out["timeline"].([]any)
	ids := map[string]bool{}
	for _, r := range timeline {
		ids[r.(map[string]any)["id"].(string)] = true
	}
	assert.True(t, ids["p/handler.go::Handle"], "handler is 2 days old — included")
	assert.True(t, ids["p/router.go::Route"], "router is 5 days old — included")
	assert.False(t, ids["p/main.go::main"], "main is 100 days old — excluded by window")
}

func TestReplayEpisode_WindowZeroDisablesFilter(t *testing.T) {
	s := newReplayTestServer(t)
	out := callReplayHandler(t, s, map[string]any{
		"anchor_symbol": "p/handler.go::Handle",
		"window_days":   0,
	})
	timeline, _ := out["timeline"].([]any)
	ids := map[string]bool{}
	for _, r := range timeline {
		ids[r.(map[string]any)["id"].(string)] = true
	}
	assert.True(t, ids["p/main.go::main"], "window_days=0 disables the time filter")
}

func TestReplayEpisode_CoverageGapsSortedAscending(t *testing.T) {
	s := newReplayTestServer(t)
	out := callReplayHandler(t, s, map[string]any{
		"anchor_symbol": "p/handler.go::Handle",
	})
	gaps, _ := out["coverage_gaps"].([]any)
	require.NotEmpty(t, gaps)

	// First row should be the lowest covered with data — handler at 30%.
	first := gaps[0].(map[string]any)
	assert.Equal(t, "p/handler.go::Handle", first["id"])
	assert.EqualValues(t, 30.0, first["coverage_pct"].(float64))
}

func TestReplayEpisode_SessionEditsCounted(t *testing.T) {
	s := newReplayTestServer(t)
	// Simulate two edits on the anchor.
	s.symHistory.Record("p/handler.go::Handle", false)
	s.symHistory.Record("p/handler.go::Handle", true)

	out := callReplayHandler(t, s, map[string]any{"anchor_symbol": "p/handler.go::Handle"})
	timeline, _ := out["timeline"].([]any)
	require.NotEmpty(t, timeline)

	var anchorRow map[string]any
	for _, r := range timeline {
		m := r.(map[string]any)
		if m["id"] == "p/handler.go::Handle" {
			anchorRow = m
			break
		}
	}
	require.NotNil(t, anchorRow)
	assert.EqualValues(t, 2, anchorRow["session_edits"].(float64))
	assert.Equal(t, true, anchorRow["signature_flux"].(bool))
}

func TestReplayEpisode_MemoriesWithIncidentTagSurfaced(t *testing.T) {
	s := newReplayTestServer(t)
	// Store one incident memory anchored to the handler; one unrelated.
	_ = callMemHandler(t, s.handleStoreMemory, map[string]any{
		"body":       "outage 2024-12-01: Handle leaked goroutines on retry",
		"kind":       "incident",
		"symbol_ids": "p/handler.go::Handle",
		"tags":       "incident,retry",
	})
	_ = callMemHandler(t, s.handleStoreMemory, map[string]any{
		"body":       "unrelated convention note",
		"kind":       "convention",
		"symbol_ids": "p/unrelated.go::Z",
	})

	out := callReplayHandler(t, s, map[string]any{"anchor_symbol": "p/handler.go::Handle"})
	mems, _ := out["memories"].([]any)
	require.Len(t, mems, 1, "only the incident memory anchored in the radius surfaces")
	m := mems[0].(map[string]any)
	assert.Equal(t, "incident", m["kind"])
}

func TestReplayEpisode_RejectsMissingAnchor(t *testing.T) {
	s := newReplayTestServer(t)
	out := callReplayHandler(t, s, map[string]any{})
	assert.True(t, out["is_error"] == true)

	out2 := callReplayHandler(t, s, map[string]any{"anchor_symbol": "does/not/exist::X"})
	assert.True(t, out2["is_error"] == true)
}

func TestReplayEpisode_DepthGatesRadius(t *testing.T) {
	s := newReplayTestServer(t)
	out := callReplayHandler(t, s, map[string]any{
		"anchor_symbol": "p/handler.go::Handle",
		"depth":         1,
	})
	// Depth 1 = anchor + direct callers only. main.go is at depth 2.
	assert.EqualValues(t, 2, out["radius_size"].(float64))
	callers, _ := out["callers"].([]any)
	require.Len(t, callers, 1)
	assert.Equal(t, "p/router.go::Route", callers[0].(map[string]any)["id"])
}
