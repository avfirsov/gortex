package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zzet/gortex/internal/graph"
)

// TestDeadCode_SyntheticExternalNodesExcluded verifies that the synthetic
// external-symbol / stub nodes the resolver materialises (stdlib::*, dep::*,
// external::* with Meta["external"]=true, and the "<kind>::*" stub ids) are
// never reported as dead code — they are imported third-party / stdlib
// symbols, not first-party code, and by construction have zero incoming
// usage edges. A real unexported function with no callers must STILL be
// reported, so the filter is specific rather than blanket.
func TestDeadCode_SyntheticExternalNodesExcluded(t *testing.T) {
	g := graph.New()

	// Synthetic external-call attribution nodes: KindFunction, lowercase
	// (unexported) names so the only thing that could exclude them is the
	// new external/stub filter — not the exported-symbol skip.
	g.AddNode(&graph.Node{
		ID: "stdlib::fmt::lowerStdlib", Kind: graph.KindFunction,
		Name: "lowerStdlib", Language: "go",
		Meta: map[string]any{"external": true},
	})
	g.AddNode(&graph.Node{
		ID: "dep::github.com/x/y::lowerDep", Kind: graph.KindFunction,
		Name: "lowerDep", Language: "go",
		Meta: map[string]any{"external": true},
	})
	g.AddNode(&graph.Node{
		ID: "external::os::lowerExternal", Kind: graph.KindFunction,
		Name: "lowerExternal", Language: "go",
		Meta: map[string]any{"external": true},
	})
	// A stub-id node WITHOUT the Meta flag — caught by graph.IsStub on the
	// id prefix alone (the CGo / stub-layer form, e.g. stdlib::C::foo).
	g.AddNode(&graph.Node{
		ID: "stdlib::C::lbug_thing", Kind: graph.KindFunction,
		Name: "lbug_thing", Language: "go",
	})
	g.AddNode(&graph.Node{
		ID: "gortex::stdlib::C::repo_prefixed_stub", Kind: graph.KindFunction,
		Name: "repo_prefixed_stub", Language: "go",
	})

	// Control: a genuine first-party unexported function with no callers.
	g.AddNode(&graph.Node{
		ID: "pkg/x.go::deadHelper", Kind: graph.KindFunction,
		Name: "deadHelper", FilePath: "pkg/x.go", StartLine: 10, EndLine: 20, Language: "go",
	})

	result := FindDeadCode(g, nil, nil)

	if assert.Len(t, result, 1, "only the real first-party dead function should be reported") {
		assert.Equal(t, "pkg/x.go::deadHelper", result[0].ID)
	}
	for _, e := range result {
		assert.False(t, graph.IsStub(e.ID), "no stub id should appear: %s", e.ID)
	}
}
