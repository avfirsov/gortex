package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/indexer"
	"github.com/zzet/gortex/internal/query"
)

func TestIndexFileFailures_SearchAndHealthRecover(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package app\n\nfunc Alpha() {}\n"), 0o644))
	g := graph.New()
	idx := indexer.New(g, testRegistry(), config.Default().Index, zap.NewNop())
	_, err := idx.Index(dir)
	require.NoError(t, err)
	srv := NewServer(query.NewEngine(g), g, idx, nil, zap.NewNop(), nil)
	require.NoError(t, g.ReplaceFileIndexFailures("", []graph.FileIndexFailure{{Path: "blocked.go", Error: "open blocked.go: permission denied", PermissionDenied: true}}))
	require.Nil(t, g.GetNode("blocked.go"), "an initial failure must not need an existing file node")

	decode := func(result *mcplib.CallToolResult) map[string]any {
		t.Helper()
		require.False(t, result.IsError)
		var payload map[string]any
		require.NoError(t, json.Unmarshal([]byte(result.Content[0].(mcplib.TextContent).Text), &payload))
		return payload
	}
	for _, tc := range []struct {
		term  string
		count float64
	}{{"Alpha", 1}, {"MissingNeedle", 0}} {
		result, err := srv.handleSearchText(context.Background(), makeReq("search_text", map[string]any{"query": tc.term, "format": "json"}))
		require.NoError(t, err)
		payload := decode(result)
		require.Equal(t, tc.count, payload["count"])
		require.Equal(t, false, payload["index_complete"])
		warning := payload["index_warning"].(map[string]any)
		require.Equal(t, float64(1), warning["unreadable_file_count"])
		require.Contains(t, warning["message"], "zero matches do not prove absence")
	}
	pathScoped, err := srv.handleSearchText(context.Background(), makeReq("search_text", map[string]any{"query": "Alpha", "format": "json", "path": "main.go"}))
	require.NoError(t, err)
	pathPayload := decode(pathScoped)
	require.Equal(t, float64(1), pathPayload["count"])
	require.NotContains(t, pathPayload, "index_warning", "a failure outside the requested path must not qualify its results")
	for _, format := range []string{"json", "compact", "toon", "gcx"} {
		args := map[string]any{"query": "Alpha", "format": format, "assist": "off"}
		if format == "compact" {
			args["compact"] = true
		}
		result, err := srv.handleSearchSymbols(context.Background(), makeReq("search_symbols", args))
		require.NoError(t, err)
		require.False(t, result.IsError)
		require.Len(t, result.Content, 1, "freshness decoration requires one text block")
		var bodies []string
		for _, content := range result.Content {
			if text, ok := content.(mcplib.TextContent); ok {
				bodies = append(bodies, text.Text)
			}
		}
		require.Contains(t, strings.Join(bodies, "\n"), "index_incomplete", format)
		if format == "json" {
			payload := decode(result)
			require.Equal(t, false, payload["truncated"], "index failures must not rewrite pagination completeness")
		}
	}
	health, err := srv.buildIndexHealthPayloadCtx(context.Background())
	require.NoError(t, err)
	require.Equal(t, "degraded", health["status"])
	require.Equal(t, 1, health["failed_file_count"])
	require.Equal(t, 1, health["unreadable_file_count"])
	require.Less(t, health["health_score"].(float64), float64(100))
	require.Contains(t, compactIndexHealth(health, time.Now(), true), "status=degraded")

	require.NoError(t, g.ReplaceFileIndexFailures("", nil))
	result, err := srv.handleSearchText(context.Background(), makeReq("search_text", map[string]any{"query": "MissingNeedle", "format": "json"}))
	require.NoError(t, err)
	require.NotContains(t, decode(result), "index_warning")
	health, err = srv.buildIndexHealthPayloadCtx(context.Background())
	require.NoError(t, err)
	require.Equal(t, "ready", health["status"])
	require.Equal(t, true, health["index_complete"])
	require.Equal(t, 0, health["failed_file_count"])
}

func TestScopedIndexFileFailures(t *testing.T) {
	failures := []indexer.FileIndexFailure{
		{Path: "alpha/svc/blocked.go", RepoPrefix: "alpha", WorkspaceID: "shared", ProjectID: "backend"},
		{Path: "beta/svc/blocked.go", RepoPrefix: "beta", WorkspaceID: "shared", ProjectID: "frontend"},
		{Path: "gamma/svc/blocked.go", RepoPrefix: "gamma", WorkspaceID: "other", ProjectID: "backend"},
		{Path: "svc/unowned.go"},
	}
	for _, tc := range []struct {
		name  string
		scope ResolvedScope
		paths []string
		want  []string
	}{
		{"repo", ResolvedScope{RepoAllow: map[string]bool{"alpha": true}}, nil, []string{"alpha/svc/blocked.go"}},
		{"workspace", ResolvedScope{WorkspaceID: "shared"}, nil, []string{"alpha/svc/blocked.go", "beta/svc/blocked.go"}},
		{"project", ResolvedScope{ProjectID: "backend"}, nil, []string{"alpha/svc/blocked.go", "gamma/svc/blocked.go"}},
		{"combined", ResolvedScope{WorkspaceID: "shared", ProjectID: "backend"}, []string{"svc"}, []string{"alpha/svc/blocked.go"}},
		{"prefixed path", ResolvedScope{}, []string{"beta/svc"}, []string{"beta/svc/blocked.go"}},
		{"path boundary", ResolvedScope{}, []string{"sv"}, nil},
		{"other path", ResolvedScope{}, []string{"tests"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var paths []string
			for _, failure := range scopedIndexFileFailures(failures, tc.scope, tc.paths) {
				paths = append(paths, failure.Path)
			}
			require.Equal(t, tc.want, paths)
		})
	}
}

func TestIndexFileFailureSummaryBoundsSamples(t *testing.T) {
	var failures []indexer.FileIndexFailure
	for i := 0; i < indexFailureSampleLimit+3; i++ {
		failures = append(failures, indexer.FileIndexFailure{Path: fmt.Sprintf("alpha/file%d.go", i), RepoPrefix: "alpha", PermissionDenied: i%2 == 0})
	}
	summary := indexFileFailureSummary(failures)
	require.Equal(t, len(failures), summary["failed_file_count"])
	require.Len(t, summary["failed_files"], indexFailureSampleLimit)
	require.Equal(t, true, summary["failed_files_truncated"])
	require.Equal(t, map[string]int{"alpha": 23}, summary["failed_files_by_repo"])
	require.Equal(t, map[string]int{"alpha": 12}, summary["unreadable_files_by_repo"])
}

func TestIndexHealthFailureCountsAvoidDoubleCountingParseErrors(t *testing.T) {
	g := graph.New()
	failures := []indexer.FileIndexFailure{{Path: "alpha/bad.go", RepoPrefix: "alpha"}}
	for _, key := range []string{"alpha/bad.go", "bad.go"} {
		total, successful := indexHealthFailureCounts(g, 2, 1, []indexer.IndexError{{FilePath: key, Error: "parse failure"}}, failures)
		require.Equal(t, 2, total)
		require.Equal(t, 1, successful)
	}
}

func TestIndexFileFailures_SelectedReaderDoesNotInheritBase(t *testing.T) {
	base, selected := graph.New(), graph.New()
	require.NoError(t, base.ReplaceFileIndexFailures("", []graph.FileIndexFailure{{Path: "base.go", Error: "permission denied", PermissionDenied: true}}))
	idx := indexer.New(base, testRegistry(), config.Default().Index, zap.NewNop())
	srv := &Server{graph: base, indexer: idx}
	require.NotNil(t, srv.indexFileFailureWarning(context.Background(), ResolvedScope{}, nil))
	ctx := withRequestView(context.Background(), &requestView{reader: selected})
	require.Nil(t, srv.indexFileFailureWarning(ctx, ResolvedScope{}, nil), "an empty selected ledger is authoritative")
	require.NoError(t, selected.ReplaceFileIndexFailures("", []graph.FileIndexFailure{{Path: "selected.go", Error: "read failed"}}))
	warning := srv.indexFileFailureWarning(ctx, ResolvedScope{}, nil)
	require.Equal(t, 1, warning["failed_file_count"])
	require.Equal(t, 0, warning["unreadable_file_count"])
	failures := warning["failed_files"].([]indexer.FileIndexFailure)
	require.Equal(t, "selected.go", failures[0].Path)
}

type unavailableIndexFailureReader struct {
	graph.Reader
	err error
}

func (r unavailableIndexFailureReader) FileIndexFailuresForRepo(string) ([]graph.FileIndexFailure, error) {
	return nil, r.err
}

func TestIndexFileFailures_ReadErrorDoesNotClaimCompleteness(t *testing.T) {
	g := graph.New()
	idx := indexer.New(g, testRegistry(), config.Default().Index, zap.NewNop())
	srv := NewServer(query.NewEngine(g), g, idx, nil, zap.NewNop(), nil)
	reader := unavailableIndexFailureReader{Reader: g, err: fmt.Errorf("ledger unavailable\nsecond line")}
	ctx := withRequestView(context.Background(), &requestView{reader: reader})
	warning := srv.indexFileFailureWarning(ctx, ResolvedScope{}, nil)
	require.NotNil(t, warning)
	require.Equal(t, 0, warning["failed_file_count"])
	require.Contains(t, warning["index_state_read_error"], "ledger unavailable")
	require.Contains(t, warning["message"], "completeness is unknown")
	result := decorateIndexFileFailureResult(mcplib.NewToolResultText("total: 0\n"), warning)
	require.Len(t, result.Content, 1)
	require.NotContains(t, result.Content[0].(mcplib.TextContent).Text, "\nsecond line", "multiline errors must remain a quoted TOON value")
	health, err := srv.buildIndexHealthPayloadCtx(ctx)
	require.NoError(t, err)
	require.Equal(t, false, health["index_complete"])
	require.Equal(t, "degraded", health["status"])
	require.Contains(t, health["index_state_read_error"], "ledger unavailable")
	require.Contains(t, compactIndexHealth(health, time.Now(), false), "status=degraded")
	require.Nil(t, srv.indexFileFailureWarning(ctx, ResolvedScope{RepoAllow: map[string]bool{"other": true}}, nil), "unselected repository read failures must not affect scoped warnings")
	idx.SetWorkspaceID("unrelated")
	idx.SetProjectID("other-project")
	require.Nil(t, srv.indexFileFailureWarning(ctx, ResolvedScope{WorkspaceID: "selected"}, nil), "another workspace's unreadable ledger must not qualify this workspace")
	require.Nil(t, srv.indexFileFailureWarning(ctx, ResolvedScope{ProjectID: "selected-project"}, nil), "project narrowing must apply before reading failure state")
	require.NotNil(t, srv.indexFileFailureWarning(ctx, ResolvedScope{WorkspaceID: "unrelated"}, nil), "an in-scope ledger error must remain visible")
}

func TestIndexFileFailures_DirectoryFailureQualifiesNestedSearch(t *testing.T) {
	g := graph.New()
	idx := indexer.New(g, testRegistry(), config.Default().Index, zap.NewNop())
	srv := NewServer(query.NewEngine(g), g, idx, nil, zap.NewNop(), nil)
	require.NoError(t, g.ReplaceFileIndexFailures("", []graph.FileIndexFailure{{Path: "restricted", Error: "permission denied", PermissionDenied: true}}))
	for _, tc := range []struct {
		path    string
		warning bool
	}{
		{"restricted/nested/file.go", true},
		{"restricted", true},
		{"restricted-other/file.go", false},
	} {
		result, err := srv.handleSearchText(context.Background(), makeReq("search_text", map[string]any{"query": "MissingNeedle", "path": tc.path, "format": "json"}))
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal([]byte(result.Content[0].(mcplib.TextContent).Text), &payload))
		require.Equal(t, float64(0), payload["count"])
		_, warned := payload["index_warning"]
		require.Equal(t, tc.warning, warned, tc.path)
	}
	failures := []indexer.FileIndexFailure{{Path: "repo/restricted", RepoPrefix: "repo"}}
	require.Len(t, scopedIndexFileFailures(failures, ResolvedScope{}, []string{"restricted/nested/file.go"}), 1)
	require.Empty(t, scopedIndexFileFailures(failures, ResolvedScope{}, []string{"restricted-other/file.go"}))
	rootFailure := []indexer.FileIndexFailure{{Path: "repo/.", RepoPrefix: "repo"}}
	require.Len(t, scopedIndexFileFailures(rootFailure, ResolvedScope{RepoAllow: map[string]bool{"repo": true}}, []string{"pkg/file.go"}), 1)
	require.Empty(t, scopedIndexFileFailures(rootFailure, ResolvedScope{RepoAllow: map[string]bool{"other": true}}, []string{"pkg/file.go"}))
	require.Len(t, scopedIndexFileFailures([]indexer.FileIndexFailure{{Path: "."}}, ResolvedScope{}, []string{"pkg/file.go"}), 1)
}

func TestIndexFileFailures_HealthCacheRefreshesLiveLedger(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package app\n\nfunc Alpha() {}\n"), 0o644))
	g := graph.New()
	idx := indexer.New(g, testRegistry(), config.Default().Index, zap.NewNop())
	_, err := idx.Index(dir)
	require.NoError(t, err)
	srv := NewServer(query.NewEngine(g), g, idx, nil, zap.NewNop(), nil)
	baseline, err := srv.buildIndexHealthBasePayloadCtx(context.Background())
	require.NoError(t, err)
	srv.indexHealth.mu.Lock()
	srv.indexHealth.payload = baseline
	srv.indexHealth.updatedAt = time.Now()
	srv.indexHealth.mu.Unlock()
	read := func(ctx context.Context) map[string]any {
		t.Helper()
		result, err := srv.handleIndexHealth(ctx, makeReq("index_health", map[string]any{"format": "json"}))
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal([]byte(result.Content[0].(mcplib.TextContent).Text), &payload))
		return payload
	}
	healthy := read(context.Background())
	require.Equal(t, true, healthy["index_complete"])
	require.NoError(t, g.ReplaceFileIndexFailures("", []graph.FileIndexFailure{{Path: "blocked.go", Error: "permission denied", PermissionDenied: true}}))
	failed := read(context.Background())
	require.Equal(t, "degraded", failed["status"])
	require.Less(t, failed["health_score"].(float64), healthy["health_score"].(float64))
	compact, err := srv.handleIndexHealth(context.Background(), makeReq("index_health", map[string]any{"compact": true}))
	require.NoError(t, err)
	require.Contains(t, compact.Content[0].(mcplib.TextContent).Text, "status=degraded")
	require.Contains(t, compact.Content[0].(mcplib.TextContent).Text, "unreadable_files=1")
	require.NotContains(t, baseline, "failed_file_count", "response-time overlay must not mutate the shared cache")
	selected := graph.New()
	selectedCtx := withRequestView(context.Background(), &requestView{reader: selected})
	require.Equal(t, true, read(selectedCtx)["index_complete"], "a healthy selected ledger must override primary failures")
	brokenCtx := withRequestView(context.Background(), &requestView{reader: unavailableIndexFailureReader{Reader: selected, err: fmt.Errorf("selected ledger unavailable")}})
	unknown := read(brokenCtx)
	require.Equal(t, false, unknown["index_complete"])
	require.Contains(t, unknown["index_state_read_error"], "selected ledger unavailable")
	require.NoError(t, g.ReplaceFileIndexFailures("", nil))
	recovered := read(context.Background())
	require.Equal(t, "ready", recovered["status"])
	require.Equal(t, healthy["health_score"], recovered["health_score"], "recovery must restore the baseline score before cache expiry")
	require.NotContains(t, recovered, "failed_files")
	require.NotContains(t, recovered, "index_state_read_error")
	require.Equal(t, healthy["recommendation"], recovered["recommendation"])
}
