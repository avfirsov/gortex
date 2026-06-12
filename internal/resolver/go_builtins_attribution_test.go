package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestAttributeGoBuiltins_FunctionCall(t *testing.T) {
	g := graph.New()
	owner := "pkg/foo.go::Run"
	g.AddNode(&graph.Node{ID: owner, Kind: graph.KindFunction, Name: "Run", FilePath: "pkg/foo.go", Language: "go"})
	edge := &graph.Edge{From: owner, To: "unresolved::append", Kind: graph.EdgeArgOf, FilePath: "pkg/foo.go", Line: 5}
	g.AddEdge(edge)

	New(g).attributeGoBuiltins()

	assert.Equal(t, "builtin::go::append", edge.To,
		"call to `append` must retarget onto builtin::go::append")
	n := g.GetNode("builtin::go::append")
	require.NotNil(t, n, "KindBuiltin node must be materialised")
	assert.Equal(t, graph.KindBuiltin, n.Kind)
	assert.Equal(t, "append", n.Name)
	assert.Equal(t, "go", n.Language)
	assert.Equal(t, true, n.Meta["builtin"])
	assert.Equal(t, "func", n.Meta["builtin_kind"])
}

func TestAttributeGoBuiltins_Type(t *testing.T) {
	g := graph.New()
	owner := "pkg/foo.go::Handler"
	g.AddNode(&graph.Node{ID: owner, Kind: graph.KindFunction, Name: "Handler", FilePath: "pkg/foo.go", Language: "go"})

	paramID := owner + "#param:s"
	g.AddNode(&graph.Node{ID: paramID, Kind: graph.KindParam, Name: "s", FilePath: "pkg/foo.go", Language: "go"})
	edge := &graph.Edge{From: paramID, To: "unresolved::string", Kind: graph.EdgeTypedAs, FilePath: "pkg/foo.go", Line: 1}
	g.AddEdge(edge)

	New(g).attributeGoBuiltins()

	assert.Equal(t, "builtin::go::type::string", edge.To,
		"typed_as `string` must retarget onto builtin::go::type::string")
	n := g.GetNode("builtin::go::type::string")
	require.NotNil(t, n)
	assert.Equal(t, graph.KindBuiltin, n.Kind)
	assert.Equal(t, "type", n.Meta["builtin_kind"])
}

func TestAttributeGoBuiltins_DedupedAcrossManyEdges(t *testing.T) {
	g := graph.New()
	owner := "pkg/foo.go::F"
	g.AddNode(&graph.Node{ID: owner, Kind: graph.KindFunction, Name: "F", FilePath: "pkg/foo.go", Language: "go"})

	// Many calls to len from the same function.
	for i := 1; i <= 5; i++ {
		g.AddEdge(&graph.Edge{From: owner, To: "unresolved::len", Kind: graph.EdgeArgOf, FilePath: "pkg/foo.go", Line: i})
	}

	New(g).attributeGoBuiltins()

	// Exactly one KindBuiltin node should be created regardless of
	// how many edges referenced it.
	count := 0
	for n := range g.NodesByKind(graph.KindBuiltin) {
		if n.ID == "builtin::go::len" {
			count++
		}
	}
	assert.Equal(t, 1, count, "exactly one KindBuiltin per unique builtin")
}

func TestAttributeGoBuiltins_NonGoLeftAlone(t *testing.T) {
	g := graph.New()
	// A Python source emitting a reference to `len` (Python builtin)
	// — must NOT get attributed to Go's `builtin::go::len`.
	owner := "pkg/app.py::process"
	g.AddNode(&graph.Node{ID: owner, Kind: graph.KindFunction, Name: "process", FilePath: "pkg/app.py", Language: "python"})
	edge := &graph.Edge{From: owner, To: "unresolved::len", Kind: graph.EdgeArgOf, FilePath: "pkg/app.py", Line: 1}
	g.AddEdge(edge)

	New(g).attributeGoBuiltins()

	assert.Equal(t, "unresolved::len", edge.To,
		"Python source must NOT cross-bind to Go's len builtin")
}

func TestAttributeGoBuiltins_UnknownNameLeftAlone(t *testing.T) {
	g := graph.New()
	owner := "pkg/foo.go::F"
	g.AddNode(&graph.Node{ID: owner, Kind: graph.KindFunction, Name: "F", FilePath: "pkg/foo.go", Language: "go"})
	edge := &graph.Edge{From: owner, To: "unresolved::myCustomFunc", Kind: graph.EdgeArgOf, FilePath: "pkg/foo.go", Line: 1}
	g.AddEdge(edge)

	New(g).attributeGoBuiltins()

	assert.Equal(t, "unresolved::myCustomFunc", edge.To,
		"non-builtin names must stay unresolved")
}

func TestAttributeGoBuiltins_QualifiedShapeLeftAlone(t *testing.T) {
	g := graph.New()
	owner := "pkg/foo.go::F"
	g.AddNode(&graph.Node{ID: owner, Kind: graph.KindFunction, Name: "F", FilePath: "pkg/foo.go", Language: "go"})

	// `*.len` is qualified — leave to other passes.
	edge := &graph.Edge{From: owner, To: "unresolved::*.len", Kind: graph.EdgeArgOf, FilePath: "pkg/foo.go", Line: 1}
	g.AddEdge(edge)

	New(g).attributeGoBuiltins()

	assert.Equal(t, "unresolved::*.len", edge.To, "qualified `*.len` shape must be left alone")
}
