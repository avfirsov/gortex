package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

// TestResolveRelativeImports_PythonStemMatchesModuleFile pins the
// "layout-aware resolution" branch of A22 for Python: a project-rooted
// stem like `app/util` lands on the existing `app/util.py` file node
// instead of staying as an `external::*` stub.
func TestResolveRelativeImports_PythonStemMatchesModuleFile(t *testing.T) {
	g := graph.New()
	seedFile(g, "app/main.py", "python")
	seedFile(g, "app/util.py", "python")
	e := seedExternalImport(g, "app/main.py", "app/util")

	r := New(g)
	r.resolveRelativeImports()

	assert.Equal(t, "app/util.py", e.To, "stem must resolve to module file ID")
}

// TestResolveRelativeImports_PythonStemMatchesPackageInit pins the
// fallback path: when `<stem>.py` doesn't exist but `<stem>/__init__.py`
// does, the edge lands on the package marker.
func TestResolveRelativeImports_PythonStemMatchesPackageInit(t *testing.T) {
	g := graph.New()
	seedFile(g, "app/main.py", "python")
	seedFile(g, "app/sub/__init__.py", "python")
	e := seedExternalImport(g, "app/main.py", "app/sub")

	r := New(g)
	r.resolveRelativeImports()

	assert.Equal(t, "app/sub/__init__.py", e.To)
}

// TestResolveRelativeImports_PythonStemUnresolvedStaysExternal — when no
// matching file exists, the edge stays external. attributeNonGoModule-
// Imports must then refuse to attribute it (no phantom pypi packages
// named after directory layout).
func TestResolveRelativeImports_PythonStemUnresolvedStaysExternal(t *testing.T) {
	g := graph.New()
	seedFile(g, "app/main.py", "python")
	e := seedExternalImport(g, "app/main.py", "app/missing")

	r := New(g)
	r.resolveRelativeImports()
	r.attributeNonGoModuleImports()

	assert.Equal(t, "external::app/missing", e.To, "unresolvable stem must stay external")
	assert.Nil(t, g.GetNode("module::pypi:app"), "must not invent a pypi package named after the dir")
}

func TestResolveRelativeImports_DartRelativeJoinsAgainstImportingDir(t *testing.T) {
	g := graph.New()
	seedFile(g, "lib/main.dart", "dart")
	seedFile(g, "lib/models/user.dart", "dart")
	e := seedExternalImport(g, "lib/main.dart", "models/user.dart")

	r := New(g)
	r.resolveRelativeImports()

	assert.Equal(t, "lib/models/user.dart", e.To)
}

func TestResolveRelativeImports_DartRelativeWithParentSegments(t *testing.T) {
	g := graph.New()
	seedFile(g, "lib/feature/main.dart", "dart")
	seedFile(g, "lib/shared/util.dart", "dart")
	e := seedExternalImport(g, "lib/feature/main.dart", "../shared/util.dart")

	r := New(g)
	r.resolveRelativeImports()

	assert.Equal(t, "lib/shared/util.dart", e.To)
}

func TestResolveRelativeImports_DartLeavesPackageURIsAlone(t *testing.T) {
	g := graph.New()
	seedFile(g, "lib/main.dart", "dart")
	e := seedExternalImport(g, "lib/main.dart", "package:flutter/material.dart")

	r := New(g)
	r.resolveRelativeImports()

	assert.Equal(t, "external::package:flutter/material.dart", e.To,
		"package: URIs are owned by the module-attribution pass, not this one")
}

func TestResolveRelativeImports_DartLeavesCoreLibraryAlone(t *testing.T) {
	g := graph.New()
	seedFile(g, "lib/main.dart", "dart")
	e := seedExternalImport(g, "lib/main.dart", "dart:async")

	r := New(g)
	r.resolveRelativeImports()

	assert.Equal(t, "external::dart:async", e.To,
		"dart: URIs are owned by the module-attribution pass, not this one")
}

// TestResolveRelativeImports_PythonAbsolutePathUnchanged guards the
// regression where the pass might over-attribute: an absolute Python
// module reference like `numpy.array` doesn't contain `/` and must
// flow through to attributeNonGoModuleImports unchanged.
func TestResolveRelativeImports_PythonAbsolutePathUnchanged(t *testing.T) {
	g := graph.New()
	seedFile(g, "app/main.py", "python")
	e := seedExternalImport(g, "app/main.py", "numpy.array")

	r := New(g)
	r.resolveRelativeImports()

	assert.Equal(t, "external::numpy.array", e.To)
}

func TestResolveRelativeImports_DartRelativeUnresolvedStaysExternal(t *testing.T) {
	g := graph.New()
	seedFile(g, "lib/main.dart", "dart")
	e := seedExternalImport(g, "lib/main.dart", "models/user.dart")

	r := New(g)
	r.resolveRelativeImports()

	assert.Equal(t, "external::models/user.dart", e.To,
		"with no target file in the graph, the edge stays external")
}

func TestResolveRelativeImports_DartEscapesRepoRoot(t *testing.T) {
	g := graph.New()
	seedFile(g, "main.dart", "dart")
	e := seedExternalImport(g, "main.dart", "../escape.dart")

	r := New(g)
	r.resolveRelativeImports()

	assert.Equal(t, "external::../escape.dart", e.To,
		"path walks above the repo root must not silently resolve")
}

func TestJoinRelativePath_VariousShapes(t *testing.T) {
	cases := []struct {
		dir  string
		rel  string
		want string
	}{
		{"lib", "models/user.dart", "lib/models/user.dart"},
		{"lib/feature", "../shared/util.dart", "lib/shared/util.dart"},
		{"lib", "./util.dart", "lib/util.dart"},
		{"lib/a/b", "../../c.dart", "lib/c.dart"},
		{"", "x.dart", "x.dart"},
		{"a", "../../escape.dart", ""},
		{"a/b/c", "../../../d.dart", "d.dart"},
		{"a/b/c", "../../../../d.dart", ""},
	}
	for _, c := range cases {
		got := joinRelativePath(c.dir, c.rel)
		require.Equal(t, c.want, got, "dir=%q rel=%q", c.dir, c.rel)
	}
}
