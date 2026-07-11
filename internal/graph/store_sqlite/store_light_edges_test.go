package store_sqlite_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

// TestAllEdgesLightIsMetaless pins the disk backend's graph.LightEdgeScanner:
// the scan must skip the meta blob (Meta == nil) — the per-edge JSON decode the
// warm-restart analysis passes exist to avoid — while every promoted field
// still equals what the full AllEdges() scan returns.
func TestAllEdgesLightIsMetaless(t *testing.T) {
	s := openTestStore(t)
	s.AddNode(&graph.Node{ID: "p/a.go::A", Kind: graph.KindFunction, Name: "A", FilePath: "p/a.go"})
	s.AddNode(&graph.Node{ID: "p/a.go::B", Kind: graph.KindFunction, Name: "B", FilePath: "p/a.go"})
	s.AddEdge(&graph.Edge{
		From: "p/a.go::A", To: "p/a.go::B", Kind: graph.EdgeCalls,
		FilePath: "p/a.go", Line: 11, Confidence: 0.75, ConfidenceLabel: "high",
		Origin: graph.OriginLSPResolved, Tier: "lsp", CrossRepo: true,
		Meta: map[string]any{"via": "direct", "blob_only": "x"},
	})
	// A second kind to prove the IN filter really scopes.
	s.AddEdge(&graph.Edge{From: "p/a.go::A", To: "p/a.go::B", Kind: graph.EdgeImports, FilePath: "p/a.go", Line: 1})

	light := s.AllEdgesLight(graph.EdgeCalls)
	require.Len(t, light, 1, "kind filter must exclude the imports edge")
	e := light[0]
	assert.Nil(t, e.Meta, "AllEdgesLight must not decode the meta blob")

	var full *graph.Edge
	for _, fe := range s.AllEdges() {
		if fe.Kind == graph.EdgeCalls {
			full = fe
		}
	}
	require.NotNil(t, full)
	assert.NotNil(t, full.Meta, "sanity: the full scan DOES decode meta")
	assert.Equal(t, full.From, e.From)
	assert.Equal(t, full.To, e.To)
	assert.Equal(t, full.Kind, e.Kind)
	assert.Equal(t, full.Line, e.Line)
	assert.Equal(t, full.Confidence, e.Confidence)
	assert.Equal(t, full.ConfidenceLabel, e.ConfidenceLabel)
	assert.Equal(t, full.Origin, e.Origin)
	assert.Equal(t, full.Tier, e.Tier)
	assert.Equal(t, full.CrossRepo, e.CrossRepo)

	// Empty kinds means every edge.
	assert.Len(t, s.AllEdgesLight(), 2)
}
