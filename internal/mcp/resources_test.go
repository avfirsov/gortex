package mcp

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordedNotification captures a single SendNotificationToAllClients call.
type recordedNotification struct {
	method string
	params map[string]any
}

// recordingNotifier implements resourcesUpdatedNotifier for tests.
type recordingNotifier struct {
	mu    sync.Mutex
	calls []recordedNotification
}

func (r *recordingNotifier) SendNotificationToAllClients(method string, params map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordedNotification{method: method, params: params})
}

func (r *recordingNotifier) snapshot() []recordedNotification {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedNotification, len(r.calls))
	copy(out, r.calls)
	return out
}

func readResource(t *testing.T, handler func(context.Context, mcplib.ReadResourceRequest) ([]mcplib.ResourceContents, error), uri string) map[string]any {
	t.Helper()
	req := mcplib.ReadResourceRequest{}
	req.Params.URI = uri
	contents, err := handler(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, contents, 1)
	tr, ok := contents[0].(mcplib.TextResourceContents)
	require.True(t, ok, "expected TextResourceContents")
	require.Equal(t, "application/json", tr.MIMEType)
	require.Equal(t, uri, tr.URI)
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(tr.Text), &payload))
	return payload
}

func TestResourceStats_MatchesGraphStatsTool(t *testing.T) {
	srv, _ := setupTestServer(t)

	resourcePayload := readResource(t, srv.handleResourceStats, "gortex://stats")

	// The resource handler must call buildGraphStatsPayload — same
	// keys the tool surfaces. Spot-check the stable keys.
	for _, key := range []string{"total_nodes", "total_edges", "by_kind", "by_language", "token_savings"} {
		assert.Contains(t, resourcePayload, key, "stats resource missing %q", key)
	}
	assert.Greater(t, int(resourcePayload["total_nodes"].(float64)), 0)
}

func TestResourceIndexHealth_HasExpectedShape(t *testing.T) {
	srv, _ := setupTestServer(t)

	payload := readResource(t, srv.handleResourceIndexHealth, "gortex://index-health")

	for _, key := range []string{"health_score", "node_count", "edge_count", "language_coverage"} {
		assert.Contains(t, payload, key, "index-health resource missing %q", key)
	}
}

func TestResourceWorkspace_DegradesUnbound(t *testing.T) {
	srv, _ := setupTestServer(t)

	payload := readResource(t, srv.handleResourceWorkspace, "gortex://workspace")

	// setupTestServer doesn't bind a workspace — expect the unbound
	// degenerate shape.
	assert.Equal(t, "unbound", payload["mode"])
	assert.NotNil(t, payload["repos"])
}

func TestResourceRepos_DegradesUnbound(t *testing.T) {
	srv, _ := setupTestServer(t)

	payload := readResource(t, srv.handleResourceRepos, "gortex://repos")

	assert.Equal(t, "unbound", payload["mode"])
	repos, ok := payload["repos"].([]any)
	require.True(t, ok)
	assert.Empty(t, repos)
}

func TestResourceActiveProject_NoConfigManager(t *testing.T) {
	srv, _ := setupTestServer(t)

	payload := readResource(t, srv.handleResourceActiveProject, "gortex://active-project")

	assert.Equal(t, "", payload["project"])
}

func TestResourceReport_HasOrientationKeys(t *testing.T) {
	srv, _ := setupTestServer(t)

	payload := readResource(t, srv.handleResourceReport, "gortex://report")

	for _, key := range []string{"total_nodes", "total_edges", "top_languages", "top_kinds", "communities", "processes", "hotspots", "dead_code", "open_todos"} {
		assert.Contains(t, payload, key, "report resource missing %q", key)
	}
}

func TestResourceGodNodes_TruncatesAt20(t *testing.T) {
	srv, _ := setupTestServer(t)

	payload := readResource(t, srv.handleResourceGodNodes, "gortex://god-nodes")

	// Either we get a list, or the small-codebase guard message.
	if msg, ok := payload["message"]; ok {
		assert.Contains(t, msg, "too small")
		return
	}
	gn, ok := payload["god_nodes"].([]any)
	require.True(t, ok)
	assert.LessOrEqual(t, len(gn), 20)
}

func TestResourceSurprises_HasExpectedKeys(t *testing.T) {
	srv, _ := setupTestServer(t)

	payload := readResource(t, srv.handleResourceSurprises, "gortex://surprises")

	for _, key := range []string{"cycles", "dead_code", "cross_community_hubs"} {
		assert.Contains(t, payload, key, "surprises resource missing %q", key)
	}
}

func TestResourceAudit_HandlesEmptyConfigDir(t *testing.T) {
	srv, _ := setupTestServer(t)

	payload := readResource(t, srv.handleResourceAudit, "gortex://audit")

	// setupTestServer's tmp dir has no CLAUDE.md / AGENTS.md /
	// Cursor rules — expect the "no agent config files" message.
	if v, ok := payload["files_scanned"]; ok {
		assert.EqualValues(t, 0, v)
	}
}

func TestResourceQuestions_HasAggregateKeys(t *testing.T) {
	srv, _ := setupTestServer(t)

	payload := readResource(t, srv.handleResourceQuestions, "gortex://questions")

	for _, key := range []string{"questions", "total", "by_tag", "with_assignee"} {
		assert.Contains(t, payload, key, "questions resource missing %q", key)
	}
}

func TestRunAnalysis_PushesBootstrapResourceUpdates(t *testing.T) {
	srv, _ := setupTestServer(t)

	rec := &recordingNotifier{}
	srv.resourcesNotifier = rec

	srv.RunAnalysis()

	calls := rec.snapshot()
	require.Equal(t, len(bootstrapResourceURIs()), len(calls), "one notification per bootstrap URI")

	// Every call must be the right method with a `uri` param matching
	// one of the bootstrap URIs.
	want := make(map[string]bool, len(bootstrapResourceURIs()))
	for _, uri := range bootstrapResourceURIs() {
		want[uri] = false
	}
	for _, c := range calls {
		assert.Equal(t, "notifications/resources/updated", c.method)
		uri, ok := c.params["uri"].(string)
		require.True(t, ok, "uri param missing or wrong type")
		_, expected := want[uri]
		assert.True(t, expected, "unexpected uri %q", uri)
		want[uri] = true
	}
	for uri, fired := range want {
		assert.True(t, fired, "bootstrap uri %q never fired", uri)
	}
}

func TestNotifyBootstrapResourcesUpdated_NoOpWithoutNotifier(t *testing.T) {
	srv, _ := setupTestServer(t)
	// Replace the live mcpServer reference with a notifier-less
	// stand-in by clearing it. The notifier path must not panic.
	srv.mcpServer = nil
	srv.notifyBootstrapResourcesUpdated()
}
