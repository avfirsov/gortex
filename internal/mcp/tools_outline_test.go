package mcp

import (
	"encoding/json"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRepoOutline_Shape(t *testing.T) {
	srv, _ := setupTestServer(t)

	result := callTool(t, srv, "get_repo_outline", nil)
	require.False(t, result.IsError)

	var resp struct {
		Summary struct {
			TotalNodes      int    `json:"total_nodes"`
			TotalEdges      int    `json:"total_edges"`
			PrimaryLanguage string `json:"primary_language"`
			Languages       []struct {
				Name  string `json:"name"`
				Nodes int    `json:"nodes"`
			} `json:"languages"`
		} `json:"summary"`
		Communities struct {
			Count int              `json:"count"`
			Top   []map[string]any `json:"top"`
		} `json:"communities"`
		Hotspots          []map[string]any `json:"hotspots"`
		MostImportedFiles []map[string]any `json:"most_imported_files"`
		EntryPoints       []map[string]any `json:"entry_points"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(mcplib.TextContent).Text), &resp))

	assert.Greater(t, resp.Summary.TotalNodes, 0, "summary must carry node count")
	assert.Greater(t, resp.Summary.TotalEdges, 0, "summary must carry edge count")
	assert.NotEmpty(t, resp.Summary.PrimaryLanguage, "primary_language must be set")
	assert.NotEmpty(t, resp.Summary.Languages, "languages list must be non-empty")
}

func TestGetRepoOutline_LanguagesSortedDescending(t *testing.T) {
	srv, _ := setupTestServer(t)

	result := callTool(t, srv, "get_repo_outline", nil)
	require.False(t, result.IsError)

	var resp struct {
		Summary struct {
			Languages []struct {
				Name  string `json:"name"`
				Nodes int    `json:"nodes"`
			} `json:"languages"`
		} `json:"summary"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(mcplib.TextContent).Text), &resp))

	for i := 0; i+1 < len(resp.Summary.Languages); i++ {
		assert.GreaterOrEqual(t,
			resp.Summary.Languages[i].Nodes,
			resp.Summary.Languages[i+1].Nodes,
			"languages must be sorted by node count descending")
	}
}

func TestGetRepoOutline_EntryPointsDetectMain(t *testing.T) {
	srv, _ := setupTestServer(t)

	result := callTool(t, srv, "get_repo_outline", nil)
	require.False(t, result.IsError)

	var resp struct {
		EntryPoints []map[string]any `json:"entry_points"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(mcplib.TextContent).Text), &resp))

	// The test fixture defines `func main()` in main.go, so at least one
	// entry point must surface.
	require.NotEmpty(t, resp.EntryPoints, "expected entry_points to include main")
	foundMain := false
	for _, ep := range resp.EntryPoints {
		if ep["name"] == "main" {
			foundMain = true
		}
	}
	assert.True(t, foundMain, "main function should be listed among entry_points")
}

func TestGetRepoOutline_StructuredNotEmpty(t *testing.T) {
	// Sanity: outline must always return every top-level key even when a
	// section is empty. Clients rely on the shape being stable so they
	// don't have to nil-check every field.
	srv, _ := setupTestServer(t)

	result := callTool(t, srv, "get_repo_outline", nil)
	require.False(t, result.IsError)

	var resp map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(mcplib.TextContent).Text), &resp))

	for _, key := range []string{"summary", "communities", "hotspots", "most_imported_files", "entry_points"} {
		_, ok := resp[key]
		assert.True(t, ok, "outline response missing top-level key %q", key)
	}
}
