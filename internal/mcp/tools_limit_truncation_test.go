package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/indexer"
	"github.com/zzet/gortex/internal/query"
)

// limitServerWith mirrors searchTextServerWith: one file per relative path,
// each defining func TargetX(), so the match / node count is the number of
// paths — which is what lets these tests reason about the limit exactly.
// It backs the #672 disclosure tests for the two tools that clamp without
// search_text's byte-budget escape hatch: find_declaration and graph_query.
func limitServerWith(t *testing.T, rels ...string) *Server {
	t.Helper()
	dir := t.TempDir()
	for i, rel := range rels {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full,
			[]byte("package app\n\nfunc Target"+string(rune('A'+i))+"() {}\n"), 0o644))
	}
	g := graph.New()
	idx := indexer.New(g, testRegistry(), config.Default().Index, zap.NewNop())
	_, err := idx.Index(dir)
	require.NoError(t, err)
	return NewServer(query.NewEngine(g), g, idx, nil, zap.NewNop(), nil)
}

func limitJSONResponse(t *testing.T, res *mcplib.CallToolResult) map[string]any {
	t.Helper()
	require.False(t, res.IsError, "%+v", res.Content)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &out))
	return out
}

func TestFindDeclaration_LimitBoundResultDisclosesTruncation(t *testing.T) {
	srv := limitServerWith(t, "a.go", "b.go", "c.go")

	res := callTool(t, srv, "find_declaration", map[string]any{"use_site": "Target", "limit": 2})
	out := limitJSONResponse(t, res)

	require.Equal(t, float64(2), out["use_sites_scanned"])
	require.Equal(t, true, out["_truncated_by_limit"],
		"a stage-1 scan bound by limit is byte-indistinguishable from a complete one without this")
	require.Equal(t, float64(2), out["_limit_applied"])
	require.Equal(t, false, out["count_is_exact"],
		"use_sites_scanned is a floor here, and saying so is the half a caller can act on")
	note, _ := out["truncation_note"].(string)
	require.Contains(t, note, "floor")
	_, present := out["_limit_requested"]
	require.False(t, present, "the caller's own limit bound this result; nothing was clamped")
}

func TestFindDeclaration_CompleteResultCarriesNoTruncationKeys(t *testing.T) {
	srv := limitServerWith(t, "a.go", "b.go", "c.go")

	res := callTool(t, srv, "find_declaration", map[string]any{"use_site": "Target"})
	out := limitJSONResponse(t, res)

	require.Equal(t, float64(3), out["use_sites_scanned"])
	for _, key := range []string{"_truncated_by_limit", "_limit_applied", "count_is_exact", "truncation_note", "_limit_requested"} {
		_, present := out[key]
		require.False(t, present, "a complete result must not carry %q", key)
	}
}

func TestGraphQuery_LimitBoundResultDisclosesTruncation(t *testing.T) {
	srv := limitServerWith(t, "a.go", "b.go", "c.go")

	res := callTool(t, srv, "graph_query", map[string]any{
		"query": "nodes kind=function", "limit": 2, "format": "json",
	})
	out := limitJSONResponse(t, res)

	require.Equal(t, float64(2), out["total_nodes"])
	bound, ok := out["_truncated_by_limit"].(map[string]any)
	require.True(t, ok, "a result bound by limit must carry the _truncated_by_limit disclosure, got %v", out["_truncated_by_limit"])
	require.Equal(t, float64(2), bound["limit_applied"])
	note, _ := bound["truncation_note"].(string)
	require.Contains(t, note, "floor")
	_, present := bound["limit_requested"]
	require.False(t, present, "the caller's own limit bound this result; nothing was clamped")
}

func TestGraphQuery_CompleteResultCarriesNoTruncationKeys(t *testing.T) {
	srv := limitServerWith(t, "a.go", "b.go", "c.go")

	res := callTool(t, srv, "graph_query", map[string]any{
		"query": "nodes kind=function", "limit": 100, "format": "json",
	})
	out := limitJSONResponse(t, res)

	require.Equal(t, float64(3), out["total_nodes"])
	_, present := out["_truncated_by_limit"]
	require.False(t, present, "a complete result must not carry the disclosure")
}

func TestBoundByLimit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		raw   int
		limit int
		want  bool
	}{
		{"corpus ran out first", 5, 10, false},
		{"landed exactly on the limit", 10, 10, true},
		// The fan-out searchers hand each repo the full limit, so the raw
		// count can exceed it before the final trim.
		{"producer returned more than the limit", 25, 10, true},
		{"no limit in force", 100, 0, false},
		{"empty result", 0, 10, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, boundByLimit(tc.raw, tc.limit))
		})
	}
}

func TestStampLimitTruncation_ReportsRequestedWhenClamped(t *testing.T) {
	resp := map[string]any{}
	stampLimitTruncation(resp, 50000, 1000, "note")

	require.Equal(t, true, resp["_truncated_by_limit"])
	require.Equal(t, 1000, resp["_limit_applied"])
	require.Equal(t, false, resp["count_is_exact"])
	require.Equal(t, "note", resp["truncation_note"])
	require.Equal(t, 50000, resp["_limit_requested"],
		"the response must say the cap, not the caller, chose the effective limit")
}

func TestStampLimitTruncation_OmitsRequestedWhenCallerChose(t *testing.T) {
	resp := map[string]any{}
	stampLimitTruncation(resp, 10, 10, "note")

	_, present := resp["_limit_requested"]
	require.False(t, present, "the caller's own limit bound this result; nothing was clamped")
}
