package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zzet/gortex/internal/graph"
)

// TestBindBareNameScopeRefs_LocalWins covers the headline case: a
// function declares a KindLocal `key1`; an EdgeReads to
// `unresolved::key1` originating from that function's body should be
// rewritten to point at the KindLocal node.
func TestBindBareNameScopeRefs_LocalWins(t *testing.T) {
	g := graph.New()
	owner := "pkg/foo.go::Handler"
	g.AddNode(&graph.Node{ID: owner, Kind: graph.KindFunction, Name: "Handler", FilePath: "pkg/foo.go", Language: "go"})

	localID := owner + "#local:key1@+3"
	g.AddNode(&graph.Node{
		ID: localID, Kind: graph.KindLocal, Name: "key1",
		FilePath: "pkg/foo.go", StartLine: 3, EndLine: 3, Language: "go",
	})
	g.AddEdge(&graph.Edge{From: localID, To: owner, Kind: graph.EdgeMemberOf, FilePath: "pkg/foo.go", Line: 3})

	edge := &graph.Edge{
		From: owner, To: "unresolved::key1",
		Kind: graph.EdgeReads, FilePath: "pkg/foo.go", Line: 5,
	}
	g.AddEdge(edge)

	r := New(g)
	r.bindBareNameScopeRefs()

	assert.Equal(t, localID, edge.To, "EdgeReads must be rewritten to the in-scope KindLocal")
}

// TestBindBareNameScopeRefs_FromBindingResolvesToOwner — the From of
// the edge is itself a per-binding ID (`<func>#local:x@+N`); the
// pass should strip the suffix to recover the enclosing function and
// still bind correctly.
func TestBindBareNameScopeRefs_FromBindingResolvesToOwner(t *testing.T) {
	g := graph.New()
	owner := "pkg/foo.go::Handler"
	g.AddNode(&graph.Node{ID: owner, Kind: graph.KindFunction, Name: "Handler", FilePath: "pkg/foo.go", Language: "go"})

	keyID := owner + "#local:key@+2"
	g.AddNode(&graph.Node{ID: keyID, Kind: graph.KindLocal, Name: "key", FilePath: "pkg/foo.go", StartLine: 2, Language: "go"})
	g.AddEdge(&graph.Edge{From: keyID, To: owner, Kind: graph.EdgeMemberOf})

	from := owner + "#local:out@+5"
	g.AddNode(&graph.Node{ID: from, Kind: graph.KindLocal, Name: "out", FilePath: "pkg/foo.go", StartLine: 5, Language: "go"})
	g.AddEdge(&graph.Edge{From: from, To: owner, Kind: graph.EdgeMemberOf})

	edge := &graph.Edge{From: from, To: "unresolved::key", Kind: graph.EdgeValueFlow, Line: 5}
	g.AddEdge(edge)

	New(g).bindBareNameScopeRefs()
	assert.Equal(t, keyID, edge.To, "From with #local: suffix must still resolve via enclosing function")
}

// TestBindBareNameScopeRefs_ParamFallback covers the Go-shadowing
// fallback: when no local matches, the parameter with the same name
// wins.
func TestBindBareNameScopeRefs_ParamFallback(t *testing.T) {
	g := graph.New()
	owner := "pkg/foo.go::Handler"
	g.AddNode(&graph.Node{ID: owner, Kind: graph.KindFunction, Name: "Handler", FilePath: "pkg/foo.go", Language: "go"})

	paramID := owner + "#param:req"
	g.AddNode(&graph.Node{ID: paramID, Kind: graph.KindParam, Name: "req", FilePath: "pkg/foo.go", Language: "go"})
	g.AddEdge(&graph.Edge{From: paramID, To: owner, Kind: graph.EdgeParamOf})

	edge := &graph.Edge{From: owner, To: "unresolved::req", Kind: graph.EdgeReads, Line: 3}
	g.AddEdge(edge)

	New(g).bindBareNameScopeRefs()
	assert.Equal(t, paramID, edge.To, "no matching local — param with same name must take over")
}

// TestBindBareNameScopeRefs_LocalShadowsParam — both a param and a
// local share the same name; the local wins (Go shadowing).
func TestBindBareNameScopeRefs_LocalShadowsParam(t *testing.T) {
	g := graph.New()
	owner := "pkg/foo.go::Handler"
	g.AddNode(&graph.Node{ID: owner, Kind: graph.KindFunction, Name: "Handler", FilePath: "pkg/foo.go", Language: "go"})

	paramID := owner + "#param:x"
	g.AddNode(&graph.Node{ID: paramID, Kind: graph.KindParam, Name: "x", FilePath: "pkg/foo.go", Language: "go"})
	g.AddEdge(&graph.Edge{From: paramID, To: owner, Kind: graph.EdgeParamOf})

	localID := owner + "#local:x@+4"
	g.AddNode(&graph.Node{ID: localID, Kind: graph.KindLocal, Name: "x", FilePath: "pkg/foo.go", StartLine: 4, Language: "go"})
	g.AddEdge(&graph.Edge{From: localID, To: owner, Kind: graph.EdgeMemberOf})

	edge := &graph.Edge{From: owner, To: "unresolved::x", Kind: graph.EdgeReads, Line: 6}
	g.AddEdge(edge)

	New(g).bindBareNameScopeRefs()
	assert.Equal(t, localID, edge.To, "KindLocal must shadow KindParam with the same name")
}

// TestBindBareNameScopeRefs_RefBeforeDeclLeftAlone — a reference
// whose line is BEFORE the local's StartLine can't possibly bind to
// that local. The pass must leave the edge unresolved rather than
// reach backwards.
func TestBindBareNameScopeRefs_RefBeforeDeclLeftAlone(t *testing.T) {
	g := graph.New()
	owner := "pkg/foo.go::Handler"
	g.AddNode(&graph.Node{ID: owner, Kind: graph.KindFunction, Name: "Handler", FilePath: "pkg/foo.go", Language: "go"})

	localID := owner + "#local:tmp@+10"
	g.AddNode(&graph.Node{ID: localID, Kind: graph.KindLocal, Name: "tmp", FilePath: "pkg/foo.go", StartLine: 10, Language: "go"})
	g.AddEdge(&graph.Edge{From: localID, To: owner, Kind: graph.EdgeMemberOf})

	edge := &graph.Edge{From: owner, To: "unresolved::tmp", Kind: graph.EdgeReads, Line: 3}
	g.AddEdge(edge)

	New(g).bindBareNameScopeRefs()
	assert.Equal(t, "unresolved::tmp", edge.To, "reference before declaration must not bind")
}

// TestBindBareNameScopeRefs_LatestShadowWins covers the standard "last
// shadow in scope" rule when two locals share a name across scopes:
// the binding declared on the higher line (closer to the reference)
// wins.
func TestBindBareNameScopeRefs_LatestShadowWins(t *testing.T) {
	g := graph.New()
	owner := "pkg/foo.go::Handler"
	g.AddNode(&graph.Node{ID: owner, Kind: graph.KindFunction, Name: "Handler", FilePath: "pkg/foo.go", Language: "go"})

	earlier := owner + "#local:err@+2"
	g.AddNode(&graph.Node{ID: earlier, Kind: graph.KindLocal, Name: "err", FilePath: "pkg/foo.go", StartLine: 2, Language: "go"})
	g.AddEdge(&graph.Edge{From: earlier, To: owner, Kind: graph.EdgeMemberOf})

	later := owner + "#local:err@+8"
	g.AddNode(&graph.Node{ID: later, Kind: graph.KindLocal, Name: "err", FilePath: "pkg/foo.go", StartLine: 8, Language: "go"})
	g.AddEdge(&graph.Edge{From: later, To: owner, Kind: graph.EdgeMemberOf})

	edge := &graph.Edge{From: owner, To: "unresolved::err", Kind: graph.EdgeReads, Line: 12}
	g.AddEdge(edge)

	New(g).bindBareNameScopeRefs()
	assert.Equal(t, later, edge.To, "the most recent shadow before the reference line must win")
}

// TestBindBareNameScopeRefs_AmbiguousLeftAlone — two locals with the
// same name declared on the same line (shouldn't happen in valid Go
// but defensive): the pass must leave the edge unresolved rather
// than pick an arbitrary winner.
func TestBindBareNameScopeRefs_AmbiguousLeftAlone(t *testing.T) {
	g := graph.New()
	owner := "pkg/foo.go::Handler"
	g.AddNode(&graph.Node{ID: owner, Kind: graph.KindFunction, Name: "Handler", FilePath: "pkg/foo.go", Language: "go"})

	a := owner + "#local:err@+5"
	b := owner + "#local:err@+5#1"
	g.AddNode(&graph.Node{ID: a, Kind: graph.KindLocal, Name: "err", FilePath: "pkg/foo.go", StartLine: 5, Language: "go"})
	g.AddNode(&graph.Node{ID: b, Kind: graph.KindLocal, Name: "err", FilePath: "pkg/foo.go", StartLine: 5, Language: "go"})
	g.AddEdge(&graph.Edge{From: a, To: owner, Kind: graph.EdgeMemberOf})
	g.AddEdge(&graph.Edge{From: b, To: owner, Kind: graph.EdgeMemberOf})

	edge := &graph.Edge{From: owner, To: "unresolved::err", Kind: graph.EdgeReads, Line: 7}
	g.AddEdge(edge)

	New(g).bindBareNameScopeRefs()
	assert.Equal(t, "unresolved::err", edge.To, "ambiguous candidates on same line must leave the edge unresolved")
}

// TestBindBareNameScopeRefs_QualifiedNotTouched ensures the pass only
// fires on bare names — qualified shapes (`*.Method`, `pkg.Name`,
// `unresolved::pyrel::...`) are left to other passes.
func TestBindBareNameScopeRefs_QualifiedNotTouched(t *testing.T) {
	g := graph.New()
	owner := "pkg/foo.go::Handler"
	g.AddNode(&graph.Node{ID: owner, Kind: graph.KindFunction, Name: "Handler", FilePath: "pkg/foo.go", Language: "go"})

	// Even if a local matches the unqualified part, the qualified
	// shapes must be left alone.
	g.AddNode(&graph.Node{ID: owner + "#local:Foo@+2", Kind: graph.KindLocal, Name: "Foo", FilePath: "pkg/foo.go", StartLine: 2, Language: "go"})
	g.AddEdge(&graph.Edge{From: owner + "#local:Foo@+2", To: owner, Kind: graph.EdgeMemberOf})

	keep := []*graph.Edge{
		{From: owner, To: "unresolved::*.Foo", Kind: graph.EdgeReads, Line: 5},
		{From: owner, To: "unresolved::pkg.Foo", Kind: graph.EdgeReads, Line: 6},
		{From: owner, To: "unresolved::pyrel::./foo", Kind: graph.EdgeReads, Line: 7},
	}
	for _, e := range keep {
		g.AddEdge(e)
	}

	New(g).bindBareNameScopeRefs()
	for _, e := range keep {
		assert.True(t,
			e.To == "unresolved::*.Foo" || e.To == "unresolved::pkg.Foo" || e.To == "unresolved::pyrel::./foo",
			"qualified shape %q must stay untouched", e.To,
		)
	}
}
