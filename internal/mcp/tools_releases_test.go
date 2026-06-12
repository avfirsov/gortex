package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

// seedReleasesGraph populates the graph with a KindRelease timeline
// and a couple of file nodes whose meta.added_in maps onto the
// releases. Mirrors what releases.EnrichGraphForBranch would have
// written; lets the read-side handler be tested without a real git
// repo.
func seedReleasesGraph(t *testing.T) *Server {
	t.Helper()
	g := graph.New()
	g.AddNode(&graph.Node{
		ID:   "release::v0.1",
		Kind: graph.KindRelease,
		Name: "v0.1",
		Meta: map[string]any{
			"tag":        "v0.1",
			"file_count": 1,
			"order":      0,
		},
	})
	g.AddNode(&graph.Node{
		ID:   "release::v0.2",
		Kind: graph.KindRelease,
		Name: "v0.2",
		Meta: map[string]any{
			"tag":        "v0.2",
			"file_count": 2,
			"order":      1,
		},
	})
	g.AddNode(&graph.Node{
		ID: "a.go", Kind: graph.KindFile, FilePath: "a.go",
		Meta: map[string]any{"added_in": "v0.1"},
	})
	g.AddNode(&graph.Node{
		ID: "b.go", Kind: graph.KindFile, FilePath: "b.go",
		Meta: map[string]any{"added_in": "v0.2"},
	})
	return &Server{
		graph:      g,
		session:    newSessionState(),
		tokenStats: &tokenStats{},
		symHistory: &symbolHistory{entries: make(map[string][]SymbolModification)},
		sessions:   newSessionMap(),
		toolScopes: newScopeRegistry(),
	}
}

func callAnalyzeReleases(t *testing.T, s *Server, args map[string]any) map[string]any {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := s.handleAnalyzeReleases(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, res)
	tc, ok := res.Content[0].(mcp.TextContent)
	require.True(t, ok)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &m))
	return m
}

func TestAnalyzeReleases_Timeline(t *testing.T) {
	s := seedReleasesGraph(t)
	out := callAnalyzeReleases(t, s, map[string]any{})
	releases, _ := out["releases"].([]any)
	require.Len(t, releases, 2)
	first := releases[0].(map[string]any)
	assert.Equal(t, "v0.1", first["tag"], "ordered by Meta.order asc — oldest first")
	assert.EqualValues(t, 0, first["order"])
	assert.EqualValues(t, 1, first["file_count"])
}

func TestAnalyzeReleases_TagFilterReturnsFiles(t *testing.T) {
	s := seedReleasesGraph(t)
	out := callAnalyzeReleases(t, s, map[string]any{"tag": "v0.2"})
	releases, _ := out["releases"].([]any)
	require.Len(t, releases, 1)
	first := releases[0].(map[string]any)
	files, _ := first["files"].([]any)
	require.Len(t, files, 1)
	assert.Equal(t, "b.go", files[0])
	assert.EqualValues(t, 1, out["file_hits"])
}

func TestAnalyzeReleases_TagFilterUnknownTag(t *testing.T) {
	s := seedReleasesGraph(t)
	out := callAnalyzeReleases(t, s, map[string]any{"tag": "v99"})
	require.NotEmpty(t, out["error"])
	assert.Equal(t, "enrich_releases", out["suggestion"])
}

func TestAnalyzeReleases_ErrorsWhenNoMeta(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{ID: "x.go", Kind: graph.KindFile, FilePath: "x.go"})
	s := &Server{
		graph:      g,
		session:    newSessionState(),
		tokenStats: &tokenStats{},
		symHistory: &symbolHistory{entries: make(map[string][]SymbolModification)},
		sessions:   newSessionMap(),
		toolScopes: newScopeRegistry(),
	}
	out := callAnalyzeReleases(t, s, map[string]any{})
	require.NotEmpty(t, out["error"])
	assert.Equal(t, "enrich_releases", out["suggestion"])
}
