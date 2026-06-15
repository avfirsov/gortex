package mcp

import (
	"context"
	"encoding/json"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/analysis"
)

func TestByDepthCounts(t *testing.T) {
	byDepth := map[int][]analysis.ImpactEntry{
		1: {{ID: "a"}, {ID: "b"}},
		2: {{ID: "c"}},
	}
	counts := byDepthCounts(byDepth)
	require.Equal(t, 2, counts[1])
	require.Equal(t, 1, counts[2])
}

func TestPageByDepth(t *testing.T) {
	byDepth := map[int][]analysis.ImpactEntry{
		1: {{ID: "a"}, {ID: "b"}},
		2: {{ID: "c"}, {ID: "d"}},
	}
	// First page of 2 (depth order): a, b -> truncated.
	paged, returned, truncated := pageByDepth(byDepth, 0, 2)
	require.Equal(t, 2, returned)
	require.True(t, truncated)
	require.Len(t, paged[1], 2)
	require.Empty(t, paged[2])

	// Offset 2, limit 2: c, d -> not truncated.
	paged, returned, truncated = pageByDepth(byDepth, 2, 2)
	require.Equal(t, 2, returned)
	require.False(t, truncated)
	require.Len(t, paged[2], 2)

	// No paging.
	_, returned, truncated = pageByDepth(byDepth, 0, 0)
	require.Equal(t, 4, returned)
	require.False(t, truncated)
}

func TestExplainChangeImpactSummaryOnly(t *testing.T) {
	srv, g := setupNavServer(t)
	bootID := navFindMethod(t, g, "boot")

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{"ids": bootID, "summary_only": true}
	res, err := srv.handleEnhancedChangeImpact(context.Background(), req)
	require.NoError(t, err)
	require.False(t, res.IsError, "explain_change_impact errored: %s", toolResultText(res))

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &payload))
	require.Contains(t, payload, "by_depth_counts", "summary should always carry counts")
	require.NotContains(t, payload, "by_depth", "summary_only should drop the heavy rows")
}
