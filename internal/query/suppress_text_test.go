package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

// suppressSubGraph builds a SubGraph whose node set is exactly the union of
// the edges' endpoints — the shape find_usages / get_callers hand to
// SuppressRedundantTextMatches.
func suppressSubGraph(edges ...*graph.Edge) *SubGraph {
	sg := &SubGraph{Edges: edges}
	seen := map[string]bool{}
	for _, e := range edges {
		for _, id := range []string{e.From, e.To} {
			if !seen[id] {
				seen[id] = true
				sg.Nodes = append(sg.Nodes, &graph.Node{ID: id})
			}
		}
	}
	return sg
}

func suppressNodeIDs(sg *SubGraph) []string {
	ids := make([]string, 0, len(sg.Nodes))
	for _, n := range sg.Nodes {
		ids = append(ids, n.ID)
	}
	return ids
}

func TestSuppressRedundantTextMatches_DropsTextWhenResolvedEvidenceExists(t *testing.T) {
	sg := suppressSubGraph(
		&graph.Edge{From: "a.go::caller", To: "b.go::T.Get", Kind: graph.EdgeCalls, Origin: graph.OriginLSPResolved},
		&graph.Edge{From: "e.go::heuristic", To: "b.go::T.Get", Kind: graph.EdgeCalls, Origin: graph.OriginASTInferred},
		&graph.Edge{From: "c.go::other", To: "b.go::T.Get", Kind: graph.EdgeCalls, Origin: graph.OriginTextMatched},
		&graph.Edge{From: "d.go::noise", To: "b.go::T.Get", Kind: graph.EdgeCalls, Origin: graph.OriginTextMatched},
	)
	sg.SuppressRedundantTextMatches()

	assert.Len(t, sg.Edges, 2)
	for _, e := range sg.Edges {
		assert.NotEqual(t, graph.OriginTextMatched, e.Origin)
	}
	assert.Equal(t, 2, sg.TextMatchedSuppressed)
	// Orphaned text-match callers pruned; surviving endpoints stay.
	assert.ElementsMatch(t,
		[]string{"a.go::caller", "e.go::heuristic", "b.go::T.Get"},
		suppressNodeIDs(sg))
}

func TestSuppressRedundantTextMatches_KeepsTextWhenOnlyEvidence(t *testing.T) {
	sg := suppressSubGraph(
		&graph.Edge{From: "c.go::other", To: "b.go::T.Get", Kind: graph.EdgeCalls, Origin: graph.OriginTextMatched},
		&graph.Edge{From: "d.go::more", To: "b.go::T.Get", Kind: graph.EdgeCalls, Origin: graph.OriginTextMatched},
	)
	sg.SuppressRedundantTextMatches()

	assert.Len(t, sg.Edges, 2)
	assert.Zero(t, sg.TextMatchedSuppressed)
	assert.Len(t, sg.Nodes, 3)
}

func TestSuppressRedundantTextMatches_KeepsUntaggedEdges(t *testing.T) {
	// An edge with no Origin stamp is backfilled by effectiveOrigin; it is
	// suppressed only if the backfill lands exactly on text_matched. Pin
	// the test to whatever the backfill says so it can't drift.
	untagged := &graph.Edge{From: "u.go::legacy", To: "b.go::T.Get", Kind: graph.EdgeCalls, Confidence: 1.0}
	require.NotEqual(t, graph.OriginTextMatched, effectiveOrigin(untagged),
		"precondition: a confidence-1.0 call edge must not backfill to text_matched")

	sg := suppressSubGraph(
		&graph.Edge{From: "a.go::caller", To: "b.go::T.Get", Kind: graph.EdgeCalls, Origin: graph.OriginLSPResolved},
		untagged,
		&graph.Edge{From: "c.go::noise", To: "b.go::T.Get", Kind: graph.EdgeCalls, Origin: graph.OriginTextMatched},
	)
	sg.SuppressRedundantTextMatches()

	assert.Len(t, sg.Edges, 2)
	assert.Equal(t, 1, sg.TextMatchedSuppressed)
	assert.ElementsMatch(t,
		[]string{"a.go::caller", "u.go::legacy", "b.go::T.Get"},
		suppressNodeIDs(sg))
}

func TestSuppressRedundantTextMatches_NilAndEmptySafe(t *testing.T) {
	var nilSG *SubGraph
	assert.NotPanics(t, func() { nilSG.SuppressRedundantTextMatches() })

	empty := &SubGraph{}
	empty.SuppressRedundantTextMatches()
	assert.Zero(t, empty.TextMatchedSuppressed)
}
