package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

// seedFile adds a KindFile node with the given language to the
// graph; tests use it to drive the language-aware attribution pass.
func seedFile(g graph.Store, fileID, language string) {
	g.AddNode(&graph.Node{
		ID: fileID, Kind: graph.KindFile, Name: fileID,
		FilePath: fileID, Language: language,
	})
}

// seedExternalImport drops in an EdgeImports edge that's already
// landed at an `external::*` target — the post-pass inputs we want
// to exercise.
func seedExternalImport(g graph.Store, fileID, importPath string) *graph.Edge {
	e := &graph.Edge{
		From:     fileID,
		To:       "external::" + importPath,
		Kind:     graph.EdgeImports,
		FilePath: fileID,
		Line:     1,
	}
	g.AddEdge(e)
	return e
}

func TestAttributeNonGo_PythonPyPI(t *testing.T) {
	g := graph.New()
	seedFile(g, "app/main.py", "python")
	e := seedExternalImport(g, "app/main.py", "requests")

	r := New(g)
	r.attributeNonGoModuleImports()

	assert.Equal(t, "module::pypi:requests", e.To, "import edge should retarget onto KindModule")
	mod := g.GetNode("module::pypi:requests")
	require.NotNil(t, mod, "KindModule node should be materialised")
	assert.Equal(t, graph.KindModule, mod.Kind)
	assert.Equal(t, "pypi", mod.Meta["ecosystem"])

	depEdges := outEdgesOfKind(g, "app/main.py", graph.EdgeDependsOnModule)
	require.Len(t, depEdges, 1, "exactly one EdgeDependsOnModule per (file, module) pair")
	assert.Equal(t, "module::pypi:requests", depEdges[0].To)
}

func TestAttributeNonGo_PythonStdlibCollapsesToTopLevel(t *testing.T) {
	g := graph.New()
	seedFile(g, "app/main.py", "python")
	// `os.path` should attribute to `os` (the top-level module),
	// not to `os.path` — a `KindModule` is the package, not the
	// dotted path.
	e := seedExternalImport(g, "app/main.py", "os.path")

	r := New(g)
	r.attributeNonGoModuleImports()

	assert.Equal(t, "module::python:stdlib::os", e.To)
	mod := g.GetNode("module::python:stdlib::os")
	require.NotNil(t, mod)
	assert.Equal(t, "python:stdlib", mod.Meta["ecosystem"])
	assert.Equal(t, "stdlib", mod.Meta["role"])
	// import_path keeps the original dotted form so consumers can
	// recover the sub-module reference.
	assert.Equal(t, "os.path", mod.Meta["import_path"])
}

func TestAttributeNonGo_PythonRelativeImportsSkipped(t *testing.T) {
	g := graph.New()
	seedFile(g, "app/main.py", "python")
	// `from . import foo` lands as `external::.` — we can't
	// attribute relative imports without package-layout context,
	// so the pass leaves them alone.
	e := seedExternalImport(g, "app/main.py", ".foo.bar")

	r := New(g)
	r.attributeNonGoModuleImports()

	assert.Equal(t, "external::.foo.bar", e.To, "relative import must remain unchanged")
	depEdges := outEdgesOfKind(g, "app/main.py", graph.EdgeDependsOnModule)
	assert.Empty(t, depEdges)
}

func TestAttributeNonGo_DartPackageURI(t *testing.T) {
	g := graph.New()
	seedFile(g, "lib/main.dart", "dart")
	e := seedExternalImport(g, "lib/main.dart", "package:flutter/material.dart")

	r := New(g)
	r.attributeNonGoModuleImports()

	assert.Equal(t, "module::pub:flutter", e.To, "package: URI maps to its top-level pub package")
	mod := g.GetNode("module::pub:flutter")
	require.NotNil(t, mod)
	assert.Equal(t, "pub", mod.Meta["ecosystem"])
}

func TestAttributeNonGo_DartCoreLibraryStdlib(t *testing.T) {
	g := graph.New()
	seedFile(g, "lib/main.dart", "dart")
	e := seedExternalImport(g, "lib/main.dart", "dart:async")

	r := New(g)
	r.attributeNonGoModuleImports()

	assert.Equal(t, "module::dart:stdlib::async", e.To)
	mod := g.GetNode("module::dart:stdlib::async")
	require.NotNil(t, mod)
	assert.Equal(t, "stdlib", mod.Meta["role"])
}

func TestAttributeNonGo_DartRelativeImportsSkipped(t *testing.T) {
	g := graph.New()
	seedFile(g, "lib/main.dart", "dart")
	e := seedExternalImport(g, "lib/main.dart", "models/user.dart")

	r := New(g)
	r.attributeNonGoModuleImports()

	assert.Equal(t, "external::models/user.dart", e.To,
		"relative-path Dart imports stay external until layout-aware resolution lands")
}

func TestAttributeNonGo_OtherLanguagesUntouched(t *testing.T) {
	g := graph.New()
	// Go imports are handled by the dep-module bridge plus
	// goanalysis; this pass must not interfere with them.
	seedFile(g, "main.go", "go")
	e := seedExternalImport(g, "main.go", "github.com/foo/bar")

	r := New(g)
	r.attributeNonGoModuleImports()

	assert.Equal(t, "external::github.com/foo/bar", e.To)
}

func TestAttributeNonGo_DeduplicatesAcrossManyImports(t *testing.T) {
	g := graph.New()
	seedFile(g, "app/a.py", "python")
	// Three Python files all importing two distinct sub-modules
	// of `numpy`. The pass must yield exactly one
	// EdgeDependsOnModule per (file, module) pair, and a single
	// shared `module::pypi:numpy` node.
	for _, p := range []string{"numpy", "numpy.linalg", "numpy.random"} {
		seedExternalImport(g, "app/a.py", p)
	}

	r := New(g)
	r.attributeNonGoModuleImports()

	mod := g.GetNode("module::pypi:numpy")
	require.NotNil(t, mod)
	deps := outEdgesOfKind(g, "app/a.py", graph.EdgeDependsOnModule)
	require.Len(t, deps, 1, "three sub-imports should collapse to one EdgeDependsOnModule")
	assert.Equal(t, "module::pypi:numpy", deps[0].To)
}

func TestAttributeNonGo_IdempotentOnSecondPass(t *testing.T) {
	g := graph.New()
	seedFile(g, "app/main.py", "python")
	seedExternalImport(g, "app/main.py", "requests")

	r := New(g)
	r.attributeNonGoModuleImports()
	r.attributeNonGoModuleImports() // run again — must not duplicate.

	deps := outEdgesOfKind(g, "app/main.py", graph.EdgeDependsOnModule)
	require.Len(t, deps, 1, "second pass must not duplicate EdgeDependsOnModule")
}

// outEdgesOfKind is a small filter over Graph.GetOutEdges for the
// assertions above; declared here to keep the test file self-
// contained.
func outEdgesOfKind(g graph.Store, fileID string, kind graph.EdgeKind) []*graph.Edge {
	var out []*graph.Edge
	for _, e := range g.GetOutEdges(fileID) {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}
