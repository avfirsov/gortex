package query

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

// Empty-origin call edges are resolver-bound usages (the common case for
// languages the resolver binds by name), not name-only text-match fan-out.
// They must survive suppression even when a stronger (LSP-confirmed) edge
// exists for the same target — the rust-analyzer partial-enrichment case
// that otherwise collapsed find_usages from hundreds of sites to a handful.
func TestSuppressRedundantTextMatches_KeepsEmptyOriginWhenResolvedExists(t *testing.T) {
	sg := suppressSubGraph(
		&graph.Edge{From: "a::caller", To: "b::foo", Kind: graph.EdgeCalls, Origin: graph.OriginLSPResolved},
		&graph.Edge{From: "c::c1", To: "b::foo", Kind: graph.EdgeCalls}, // empty origin — resolver-bound
		&graph.Edge{From: "d::c2", To: "b::foo", Kind: graph.EdgeCalls}, // empty origin — resolver-bound
		&graph.Edge{From: "e::noise", To: "b::foo", Kind: graph.EdgeCalls, Origin: graph.OriginTextMatched},
	)
	sg.SuppressRedundantTextMatches()

	// Only the explicitly text_matched edge is dropped.
	require.Equal(t, 3, len(sg.Edges))
	require.Equal(t, 1, sg.TextMatchedSuppressed)
	for _, e := range sg.Edges {
		require.NotEqual(t, graph.OriginTextMatched, e.Origin)
	}
}
