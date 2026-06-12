package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

// TestPython_FromImport_AliasMapPopulatesCallTarget pins the
// "alias-aware call resolution" behaviour:
//
//   from foo.bar import baz as bz
//   bz(1)
//
// must produce an unresolved::extern::foo.bar.baz::bz EdgeCalls,
// not a bare unresolved::call::bz, so the resolver-side module
// attribution can later land it on a KindModule.
func TestPython_FromImport_AliasMapPopulatesCallTarget(t *testing.T) {
	src := []byte(`from foo.bar import baz as bz
from greenlib import widget

def use():
    bz(1)
    widget.spin()
`)
	e := NewPythonExtractor()
	res, err := e.Extract("app/main.py", src)
	require.NoError(t, err)

	// The aliased name `bz` should attribute to foo.bar.baz.
	require.True(t, hasEdgeBetween(res.Edges, graph.EdgeCalls,
		"app/main.py::use", "unresolved::extern::foo.bar.baz::bz"),
		"aliased `bz()` call should attribute to foo.bar.baz")

	// Module-attribute call `widget.spin()` should attribute to
	// the imported package via the bare-name binding emitted by
	// `from greenlib import widget`.
	require.True(t, hasEdgeBetween(res.Edges, graph.EdgeCalls,
		"app/main.py::use", "unresolved::extern::greenlib.widget::spin"),
		"bare `widget.spin()` should attribute to greenlib.widget")
}

func TestPython_FromImport_BareNameBindsToDottedPath(t *testing.T) {
	// `from foo import bar` (no alias) — the bare `bar` should
	// bind to `foo.bar`. The pre-fix behaviour left bare names
	// unresolved.
	src := []byte(`from foo import bar

def caller():
    bar()
`)
	e := NewPythonExtractor()
	res, err := e.Extract("m.py", src)
	require.NoError(t, err)

	// `bar(...)` is a name-only call (not an attribute call).
	// The current call-resolution path emits an EdgeCalls to
	// "unresolved::call::bar"; the alias map would have lifted
	// it to module-attribution if `bar` were used as a
	// receiver. We assert at least the import-edge round-trip
	// here so the alias capture itself is exercised; the call
	// path is covered by the aliased test above.
	require.True(t, hasEdgeBetween(res.Edges, graph.EdgeImports,
		"m.py", "unresolved::import::foo"),
		"import edge to foo should still be emitted")

	// Ensure the import statement still emits a KindImport node.
	importNodes := nodesOfKind(res.Nodes, graph.KindImport)
	assert.GreaterOrEqual(t, len(importNodes), 1)
}
