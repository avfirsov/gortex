package mcp

import (
	"context"
	"encoding/json"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/analysis"
	"github.com/zzet/gortex/internal/graph"
)

func TestCommonDirPrefix(t *testing.T) {
	require.Equal(t, "internal/parser", commonDirPrefix([]string{"internal/parser/a.go", "internal/parser/b.go"}))
	require.Equal(t, "internal", commonDirPrefix([]string{"internal/parser/a.go", "internal/graph/g.go"}))
	require.Equal(t, "", commonDirPrefix([]string{"internal/parser/a.go", "cmd/main.go"}))
	require.Equal(t, "", commonDirPrefix([]string{"main.go"}))
	require.Equal(t, "internal/parser", commonDirPrefix([]string{"internal/parser/a.go"}))
}

func TestSanitizeAndUniqueLayerName(t *testing.T) {
	require.Equal(t, "internal_parser", sanitizeLayerName("internal/parser"))
	require.Equal(t, "pkg_auth", sanitizeLayerName("a/b/pkg/auth"))
	require.Equal(t, "my_svc", sanitizeLayerName("my-svc"))

	used := map[string]bool{}
	n1 := uniqueLayerName("internal/parser", "", used)
	used[n1] = true
	n2 := uniqueLayerName("internal/parser", "", used)
	require.Equal(t, "internal_parser", n1)
	require.Equal(t, "internal_parser_2", n2)
}

func TestRenderArchitectureYAML(t *testing.T) {
	out := renderArchitectureYAML([]suggestedLayer{
		{Name: "core", Paths: []string{"internal/core/**"}, Allow: []string{"util"}},
		{Name: "util", Paths: []string{"internal/util/**"}, Allow: nil},
	})
	require.Contains(t, out, "architecture:")
	require.Contains(t, out, "core:")
	require.Contains(t, out, `allow: ["util"]`)
	require.Contains(t, out, "allow: []")
}

func TestSuggestBoundariesHandler(t *testing.T) {
	srv, g := setupNavServer(t)

	// Inject two synthetic communities with an observed cross-edge.
	g.AddNode(&graph.Node{ID: "internal/parser/a.go::A", Name: "A", Kind: graph.KindFunction, FilePath: "internal/parser/a.go"})
	g.AddNode(&graph.Node{ID: "internal/graph/g.go::G", Name: "G", Kind: graph.KindFunction, FilePath: "internal/graph/g.go"})
	g.AddEdge(&graph.Edge{From: "internal/parser/a.go::A", To: "internal/graph/g.go::G", Kind: graph.EdgeCalls})

	srv.communities = &analysis.CommunityResult{
		Communities: []analysis.Community{
			{ID: "c1", Label: "parser", Members: []string{"internal/parser/a.go::A"}, Files: []string{"internal/parser/a.go"}, Size: 4},
			{ID: "c2", Label: "graph", Members: []string{"internal/graph/g.go::G"}, Files: []string{"internal/graph/g.go"}, Size: 5},
		},
		NodeToComm: map[string]string{
			"internal/parser/a.go::A": "c1",
			"internal/graph/g.go::G":  "c2",
		},
		Modularity: 0.5,
	}

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{"kind": "suggest_boundaries"}
	res, err := srv.handleSuggestBoundaries(context.Background(), req)
	require.NoError(t, err)
	require.False(t, res.IsError, "suggest_boundaries errored: %s", toolResultText(res))

	var payload struct {
		SuggestedLayers []suggestedLayer `json:"suggested_layers"`
		YAML            string           `json:"yaml"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &payload))
	require.Len(t, payload.SuggestedLayers, 2)

	byName := map[string]suggestedLayer{}
	for _, l := range payload.SuggestedLayers {
		byName[l.Name] = l
	}
	parser, ok := byName["internal_parser"]
	require.True(t, ok, "expected an internal_parser layer, got %v", byName)
	require.Equal(t, []string{"internal/parser/**"}, parser.Paths)
	require.Contains(t, parser.Allow, "internal_graph", "observed parser->graph edge should seed an allow")
	require.Contains(t, payload.YAML, "architecture:")
}
