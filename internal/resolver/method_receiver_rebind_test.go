package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

// TestRebindGoMethodReceivers_CollapsesCrossFileMethods is the
// regression for the Go extractor emitting EdgeMemberOf targets as
// <methodfile>::TypeName. When methods on the same type live in
// different files of the same package, the parser produces a phantom
// type ID per method-file; the rebind pass must collapse them onto
// the canonical <typefile>::TypeName node so InferImplements and the
// downstream MCP tools (find_implementations, class_hierarchy) see
// the consolidated method set.
func TestRebindGoMethodReceivers_CollapsesCrossFileMethods(t *testing.T) {
	g := graph.New()

	// Type defined in indexer.go.
	typeID := "internal/indexer/indexer.go::Indexer"
	g.AddNode(&graph.Node{
		ID: typeID, Kind: graph.KindType, Name: "Indexer",
		FilePath: "internal/indexer/indexer.go", Language: "go",
	})

	// Method declared in a *different* file in the same package — the
	// parser emits a phantom receiver target.
	methodID := "internal/indexer/crash_isolation.go::Indexer.crashIsolationEnabled"
	g.AddNode(&graph.Node{
		ID: methodID, Kind: graph.KindMethod, Name: "crashIsolationEnabled",
		FilePath: "internal/indexer/crash_isolation.go", Language: "go",
	})
	phantomTarget := "internal/indexer/crash_isolation.go::Indexer"
	memberEdge := &graph.Edge{
		From: methodID, To: phantomTarget, Kind: graph.EdgeMemberOf,
		FilePath: "internal/indexer/crash_isolation.go", Line: 23,
	}
	g.AddEdge(memberEdge)

	// Sanity: pre-pass the phantom target has no real node.
	require.Nil(t, g.GetNode(phantomTarget), "phantom target must not exist as a real node")

	r := New(g)
	r.rebindGoMethodReceivers()

	// Post-pass: the edge points at the canonical type node.
	assert.Equal(t, typeID, memberEdge.To,
		"EdgeMemberOf must be rewritten from <methodfile>::Type to canonical <typefile>::Type")

	// And the same-file method on the type works too — covered by not
	// breaking a control case:
	g2 := graph.New()
	g2.AddNode(&graph.Node{
		ID: "pkg/foo.go::Foo", Kind: graph.KindType, Name: "Foo",
		FilePath: "pkg/foo.go", Language: "go",
	})
	g2.AddNode(&graph.Node{
		ID: "pkg/foo.go::Foo.Bar", Kind: graph.KindMethod, Name: "Bar",
		FilePath: "pkg/foo.go", Language: "go",
	})
	sameFileEdge := &graph.Edge{
		From: "pkg/foo.go::Foo.Bar", To: "pkg/foo.go::Foo",
		Kind: graph.EdgeMemberOf, FilePath: "pkg/foo.go", Line: 5,
	}
	g2.AddEdge(sameFileEdge)

	New(g2).rebindGoMethodReceivers()
	assert.Equal(t, "pkg/foo.go::Foo", sameFileEdge.To,
		"same-file method edge must be left unchanged")
}

// TestRebindGoMethodReceivers_LanguageGated guards against the pass
// rewriting non-Go EdgeMemberOf edges. Java/TS/Python group methods
// in the class body so their EdgeMemberOf targets are already
// in-file; we don't want the pass touching them.
func TestRebindGoMethodReceivers_LanguageGated(t *testing.T) {
	g := graph.New()

	// A type and a method in the same Go package — would normally be
	// a rebind candidate.
	g.AddNode(&graph.Node{
		ID: "pkg/types.go::Server", Kind: graph.KindType, Name: "Server",
		FilePath: "pkg/types.go", Language: "go",
	})
	// But the METHOD is declared as TypeScript (e.g. a TS extractor
	// that emits the same EdgeMemberOf shape for some bridging
	// reason). Pass must leave it alone.
	tsMethod := &graph.Node{
		ID: "pkg/handler.ts::Server.serve", Kind: graph.KindMethod, Name: "serve",
		FilePath: "pkg/handler.ts", Language: "typescript",
	}
	g.AddNode(tsMethod)
	edge := &graph.Edge{
		From: tsMethod.ID, To: "pkg/handler.ts::Server",
		Kind: graph.EdgeMemberOf, FilePath: "pkg/handler.ts", Line: 1,
	}
	g.AddEdge(edge)

	New(g).rebindGoMethodReceivers()
	assert.Equal(t, "pkg/handler.ts::Server", edge.To,
		"non-Go method edge must NOT be rewritten by the Go-only rebind pass")
}

// TestRebindGoMethodReceivers_AmbiguousNameSkipped guards against the
// pass picking an arbitrary winner when two distinct types share the
// same name in the same package (shouldn't happen in valid Go, but
// the pass should leave the phantom alone rather than mis-bind).
func TestRebindGoMethodReceivers_AmbiguousNameSkipped(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{
		ID: "pkg/a.go::Dup", Kind: graph.KindType, Name: "Dup",
		FilePath: "pkg/a.go", Language: "go",
	})
	g.AddNode(&graph.Node{
		ID: "pkg/b.go::Dup", Kind: graph.KindType, Name: "Dup",
		FilePath: "pkg/b.go", Language: "go",
	})
	g.AddNode(&graph.Node{
		ID: "pkg/c.go::Dup.M", Kind: graph.KindMethod, Name: "M",
		FilePath: "pkg/c.go", Language: "go",
	})
	edge := &graph.Edge{
		From: "pkg/c.go::Dup.M", To: "pkg/c.go::Dup",
		Kind: graph.EdgeMemberOf, FilePath: "pkg/c.go", Line: 1,
	}
	g.AddEdge(edge)

	New(g).rebindGoMethodReceivers()
	assert.Equal(t, "pkg/c.go::Dup", edge.To,
		"ambiguous type name in same package must leave the edge phantom rather than guess")
}
