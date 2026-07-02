package mcp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/semantic"
)

// fakeSemProvider is a minimal semantic.Provider whose Enrich returns a
// canned result — enough to drive Manager.EnrichAll and populate the
// per-(repo, provider) enrichment statuses the health payload surfaces.
type fakeSemProvider struct {
	name   string
	result *semantic.EnrichResult
}

func (f *fakeSemProvider) Name() string        { return f.name }
func (f *fakeSemProvider) Languages() []string { return []string{"go"} }
func (f *fakeSemProvider) Available() bool     { return true }
func (f *fakeSemProvider) Close() error        { return nil }
func (f *fakeSemProvider) Enrich(g graph.Store, repoRoot string) (*semantic.EnrichResult, error) {
	return f.result, nil
}
func (f *fakeSemProvider) EnrichFile(g graph.Store, repoRoot, filePath string) (*semantic.EnrichResult, error) {
	return nil, nil
}

// TestIndexHealth_SurfacesSemanticEnrichmentState verifies index_health
// exposes the per-repo, per-provider enrichment lifecycle — a graph that
// parses 100% green but whose LSP pass was cut must be visibly partial,
// with a recommendation, instead of silently reporting healthy.
func TestIndexHealth_SurfacesSemanticEnrichmentState(t *testing.T) {
	srv, _ := setupTestServer(t)

	mgr := semantic.NewManager(semantic.Config{
		Enabled: true,
		Providers: []semantic.ProviderConfig{
			{Name: "lsp-fake", Languages: []string{"go"}, Priority: 1, Enabled: true},
		},
	}, zap.NewNop())
	mgr.RegisterProvider(&fakeSemProvider{
		name: "lsp-fake",
		result: &semantic.EnrichResult{
			Provider:       "lsp-fake",
			Language:       "go",
			EdgesConfirmed: 4,
			NodesEnriched:  9,
			Partial:        true,
			AbortReason:    "context deadline exceeded",
		},
	})
	_, err := mgr.EnrichAll(srv.graph, map[string]string{"repo-a": t.TempDir()})
	require.NoError(t, err)
	srv.SetSemanticManager(mgr)

	payload := srv.buildIndexHealthPayload()
	require.NotNil(t, payload)

	statuses, ok := payload["semantic_enrichment"].([]semantic.EnrichmentStatus)
	require.True(t, ok, "index_health must carry the semantic_enrichment statuses")
	require.Len(t, statuses, 1)
	assert.Equal(t, "repo-a", statuses[0].Repo)
	assert.Equal(t, "lsp-fake", statuses[0].Provider)
	assert.Equal(t, semantic.EnrichStatePartial, statuses[0].State)
	assert.Equal(t, 4, statuses[0].EdgesConfirmed)
	assert.Equal(t, 9, statuses[0].NodesEnriched)

	okFlag, ok := payload["semantic_enrichment_ok"].(bool)
	require.True(t, ok)
	assert.False(t, okFlag, "a partial pass must flip semantic_enrichment_ok to false")

	rec, _ := payload["recommendation"].(string)
	assert.True(t, strings.Contains(rec, "Semantic enrichment"),
		"partial enrichment must surface a recommendation, got: %q", rec)
}

// TestIndexHealth_SemanticEnrichmentCompletedIsGreen verifies a fully
// completed pass reports ok=true and adds no enrichment recommendation.
func TestIndexHealth_SemanticEnrichmentCompletedIsGreen(t *testing.T) {
	srv, _ := setupTestServer(t)

	mgr := semantic.NewManager(semantic.Config{
		Enabled: true,
		Providers: []semantic.ProviderConfig{
			{Name: "lsp-fake", Languages: []string{"go"}, Priority: 1, Enabled: true},
		},
	}, zap.NewNop())
	mgr.RegisterProvider(&fakeSemProvider{
		name:   "lsp-fake",
		result: &semantic.EnrichResult{Provider: "lsp-fake", Language: "go", EdgesConfirmed: 2},
	})
	_, err := mgr.EnrichAll(srv.graph, map[string]string{"repo-a": t.TempDir()})
	require.NoError(t, err)
	srv.SetSemanticManager(mgr)

	payload := srv.buildIndexHealthPayload()
	require.NotNil(t, payload)

	okFlag, ok := payload["semantic_enrichment_ok"].(bool)
	require.True(t, ok)
	assert.True(t, okFlag)
	rec, _ := payload["recommendation"].(string)
	assert.False(t, strings.Contains(rec, "Semantic enrichment"), "completed enrichment must not warn, got: %q", rec)
}
