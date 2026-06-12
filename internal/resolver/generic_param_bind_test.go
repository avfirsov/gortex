package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zzet/gortex/internal/graph"
)

func TestBindGenericParamRefs_RewritesTRefToTParam(t *testing.T) {
	g := graph.New()
	owner := "pkg/foo.go::Map"
	g.AddNode(&graph.Node{ID: owner, Kind: graph.KindFunction, Name: "Map", FilePath: "pkg/foo.go", Language: "go"})

	tparamID := owner + "#tparam:T"
	g.AddNode(&graph.Node{ID: tparamID, Kind: graph.KindGenericParam, Name: "T", FilePath: "pkg/foo.go", Language: "go"})
	g.AddEdge(&graph.Edge{From: tparamID, To: owner, Kind: graph.EdgeMemberOf})

	// `var x T` inside Map's body — EdgeTypedAs from a local-ish
	// source to the unresolved-T target.
	from := owner + "#local:x@+3"
	g.AddNode(&graph.Node{ID: from, Kind: graph.KindLocal, Name: "x", FilePath: "pkg/foo.go", StartLine: 3, Language: "go"})
	edge := &graph.Edge{From: from, To: "unresolved::T", Kind: graph.EdgeTypedAs, Line: 3}
	g.AddEdge(edge)

	New(g).bindGenericParamRefs()
	assert.Equal(t, tparamID, edge.To, "var x T must bind to the function's KindGenericParam T")
}

func TestBindGenericParamRefs_OtherFunctionsLeftAlone(t *testing.T) {
	g := graph.New()
	// Function A declares tparam T.
	a := "pkg/a.go::A"
	g.AddNode(&graph.Node{ID: a, Kind: graph.KindFunction, Name: "A", FilePath: "pkg/a.go", Language: "go"})
	g.AddNode(&graph.Node{ID: a + "#tparam:T", Kind: graph.KindGenericParam, Name: "T", FilePath: "pkg/a.go", Language: "go"})
	g.AddEdge(&graph.Edge{From: a + "#tparam:T", To: a, Kind: graph.EdgeMemberOf})

	// Function B has its OWN body and references `T`, but doesn't
	// declare it. Pass must NOT bind to A's tparam.
	b := "pkg/b.go::B"
	g.AddNode(&graph.Node{ID: b, Kind: graph.KindFunction, Name: "B", FilePath: "pkg/b.go", Language: "go"})
	edge := &graph.Edge{From: b, To: "unresolved::T", Kind: graph.EdgeReferences, Line: 1}
	g.AddEdge(edge)

	New(g).bindGenericParamRefs()
	assert.Equal(t, "unresolved::T", edge.To, "must not cross-bind to another function's tparam")
}

func TestBindGenericParamRefs_QualifiedShapesIgnored(t *testing.T) {
	g := graph.New()
	owner := "pkg/foo.go::F"
	g.AddNode(&graph.Node{ID: owner, Kind: graph.KindFunction, Name: "F", FilePath: "pkg/foo.go", Language: "go"})
	g.AddNode(&graph.Node{ID: owner + "#tparam:T", Kind: graph.KindGenericParam, Name: "T", FilePath: "pkg/foo.go", Language: "go"})
	g.AddEdge(&graph.Edge{From: owner + "#tparam:T", To: owner, Kind: graph.EdgeMemberOf})

	keep := []*graph.Edge{
		{From: owner, To: "unresolved::*.T", Kind: graph.EdgeReferences, Line: 1},
		{From: owner, To: "unresolved::pkg.T", Kind: graph.EdgeReferences, Line: 2},
	}
	for _, e := range keep {
		g.AddEdge(e)
	}
	New(g).bindGenericParamRefs()
	for _, e := range keep {
		assert.True(t,
			e.To == "unresolved::*.T" || e.To == "unresolved::pkg.T",
			"qualified shape %q must be left alone", e.To,
		)
	}
}
