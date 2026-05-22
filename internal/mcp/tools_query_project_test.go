package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

// TestQueryProject_CrossesWorkspaceWithoutMutatingState verifies
// query_project reaches another repo from a workspace-bound session and
// leaves the active project untouched.
func TestQueryProject_CrossesWorkspaceWithoutMutatingState(t *testing.T) {
	srv, repoA, _ := newIsolationServer(t)
	ctx := sessionCtx("s-alpha", repoA) // a session bound to workspace alpha
	before := srv.activeProject

	req := mcplib.CallToolRequest{}
	req.Params.Name = "query_project"
	req.Params.Arguments = map[string]any{"project": "repo-b", "query": "BetaThing"}
	res, err := srv.handleQueryProject(ctx, req)
	require.NoError(t, err)
	require.False(t, res.IsError, "query_project must reach repo-b from an alpha-bound session")

	var resp map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &resp))
	results, _ := resp["results"].([]any)
	require.NotEmpty(t, results, "expected the BetaThing symbol from repo-b")

	foundBeta := false
	for _, r := range results {
		id, _ := r.(map[string]any)["id"].(string)
		if strings.Contains(id, "BetaThing") {
			foundBeta = true
		}
		require.NotContainsf(t, id, "AlphaThing", "query_project for repo-b leaked a result from repo-a: %s", id)
	}
	require.True(t, foundBeta, "query_project must return the target project's symbol")

	require.Equal(t, before, srv.activeProject, "query_project must not mutate the active project")
}

func TestQueryProject_UnknownTargetErrors(t *testing.T) {
	srv, _, _ := newIsolationServer(t)
	req := mcplib.CallToolRequest{}
	req.Params.Name = "query_project"
	req.Params.Arguments = map[string]any{"project": "no-such-project", "query": "x"}
	res, err := srv.handleQueryProject(context.Background(), req)
	require.NoError(t, err)
	require.True(t, res.IsError, "an unknown project or repo must error")
}
