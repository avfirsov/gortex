package goanalysis

import (
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/graph"
)

// resolvedTempDir wraps t.TempDir() with EvalSymlinks because on macOS
// t.TempDir() returns /var/folders/... while go/packages reports paths
// as /private/var/folders/... — the relativePath() prefix check would
// otherwise drop every file.
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return dir
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}

func writeGoMod(t *testing.T, dir, module string) {
	t.Helper()
	writeFile(t, dir, "go.mod", "module "+module+"\n\ngo 1.21\n")
}

// twoPackageCallerFixture writes a 2-package module where pkg/b/b.go calls
// pkg/a/a.go's Hello function. Used by the call-edge tests.
func twoPackageCallerFixture(t *testing.T, root string) {
	t.Helper()
	writeGoMod(t, root, "example.com/test")
	writeFile(t, root, "pkg/a/a.go", `package a

func Hello() string {
	return "hi"
}
`)
	writeFile(t, root, "pkg/b/b.go", `package b

import "example.com/test/pkg/a"

func Caller() string {
	return a.Hello()
}
`)
}

func newTestProvider(t *testing.T) *Provider {
	t.Helper()
	return NewProvider(ModeTypeCheck, false, zap.NewNop())
}

func TestGoAnalysis_Available(t *testing.T) {
	p := newTestProvider(t)
	assert.True(t, p.Available(), "go toolchain should be on PATH in tests")
	assert.Equal(t, "go-types", p.Name())
	assert.Equal(t, []string{"go"}, p.Languages())
}

func TestGoAnalysis_RelativePath(t *testing.T) {
	tests := []struct {
		name    string
		absPath string
		root    string
		want    string
	}{
		{"inside root", "/repo/pkg/a/a.go", "/repo", "pkg/a/a.go"},
		{"at root", "/repo/main.go", "/repo", "main.go"},
		{"outside root returns empty", "/elsewhere/foo.go", "/repo", ""},
		{"empty repo", "/repo/main.go", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := relativePath(tt.absPath, tt.root)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGoAnalysis_FindContainingFunc_PicksSmallest(t *testing.T) {
	g := graph.New()
	// Outer function spans lines 10-30.
	g.AddNode(&graph.Node{
		ID: "main.go::Outer", Kind: graph.KindFunction, Name: "Outer",
		FilePath: "main.go", StartLine: 10, EndLine: 30, Language: "go",
	})
	// Inner method spans lines 15-20 (smaller, should win for line 17).
	g.AddNode(&graph.Node{
		ID: "main.go::Inner", Kind: graph.KindMethod, Name: "Inner",
		FilePath: "main.go", StartLine: 15, EndLine: 20, Language: "go",
	})

	pos := token.Position{Filename: "/repo/main.go", Line: 17}
	got := findContainingFunc(g, nil, nil, "/repo", pos)
	require.NotNil(t, got)
	assert.Equal(t, "main.go::Inner", got.ID)

	// Line 25 is inside Outer only.
	pos.Line = 25
	got = findContainingFunc(g, nil, nil, "/repo", pos)
	require.NotNil(t, got)
	assert.Equal(t, "main.go::Outer", got.ID)

	// Line 5 is in neither.
	pos.Line = 5
	got = findContainingFunc(g, nil, nil, "/repo", pos)
	assert.Nil(t, got)
}

func TestGoAnalysis_InferEdgeKindFromObj(t *testing.T) {
	// Construct minimal go/types objects via a real package load so we get
	// real *types.Func / *types.TypeName / *types.Var / *types.Const values.
	root := resolvedTempDir(t)
	writeGoMod(t, root, "example.com/kindtest")
	writeFile(t, root, "main.go", `package main

const Pi = 3.14

var Counter int

type Widget struct{}

func Make() *Widget {
	return &Widget{}
}
`)

	p := newTestProvider(t)
	pkgs, _, err := p.loadPackages(root)
	require.NoError(t, err)
	require.NotEmpty(t, pkgs)

	var pi, counter, widget, make_ types.Object
	for _, pkg := range pkgs {
		if pkg.TypesInfo == nil {
			continue
		}
		for _, obj := range pkg.TypesInfo.Defs {
			if obj == nil {
				continue
			}
			switch obj.Name() {
			case "Pi":
				pi = obj
			case "Counter":
				counter = obj
			case "Widget":
				widget = obj
			case "Make":
				make_ = obj
			}
		}
	}
	require.NotNil(t, pi)
	require.NotNil(t, counter)
	require.NotNil(t, widget)
	require.NotNil(t, make_)

	assert.Equal(t, graph.EdgeCalls, inferEdgeKindFromObj(make_))
	assert.Equal(t, graph.EdgeReferences, inferEdgeKindFromObj(widget))
	assert.Equal(t, graph.EdgeReferences, inferEdgeKindFromObj(counter))
	assert.Equal(t, graph.EdgeReferences, inferEdgeKindFromObj(pi))
}

func TestGoAnalysis_LoadPackages_Smoke(t *testing.T) {
	root := resolvedTempDir(t)
	twoPackageCallerFixture(t, root)

	p := newTestProvider(t)
	pkgs, fset, err := p.loadPackages(root)
	require.NoError(t, err)
	require.NotEmpty(t, pkgs)
	require.NotNil(t, fset)

	var sawA, sawB bool
	for _, pkg := range pkgs {
		require.NotNil(t, pkg.TypesInfo, "every returned package must have TypesInfo")
		switch pkg.PkgPath {
		case "example.com/test/pkg/a":
			sawA = true
		case "example.com/test/pkg/b":
			sawB = true
		}
	}
	assert.True(t, sawA, "expected pkg a to be loaded")
	assert.True(t, sawB, "expected pkg b to be loaded")
}

func TestGoAnalysis_ConfirmsCallEdge(t *testing.T) {
	root := resolvedTempDir(t)
	twoPackageCallerFixture(t, root)

	g := graph.New()
	g.AddNode(&graph.Node{
		ID: "pkg/a/a.go::Hello", Kind: graph.KindFunction, Name: "Hello",
		FilePath: "pkg/a/a.go", StartLine: 3, EndLine: 5, Language: "go",
	})
	g.AddNode(&graph.Node{
		ID: "pkg/b/b.go::Caller", Kind: graph.KindFunction, Name: "Caller",
		FilePath: "pkg/b/b.go", StartLine: 5, EndLine: 7, Language: "go",
	})

	// Pre-seed an INFERRED call edge that go-types should confirm.
	g.AddEdge(&graph.Edge{
		From: "pkg/b/b.go::Caller", To: "pkg/a/a.go::Hello", Kind: graph.EdgeCalls,
		Confidence: 0.7, ConfidenceLabel: "INFERRED",
		Origin: graph.OriginASTInferred,
	})

	p := newTestProvider(t)
	result, err := p.Enrich(g, root)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Greater(t, result.SymbolsCovered, 0, "should map at least Hello and Caller")
	assert.GreaterOrEqual(t, result.EdgesConfirmed, 1, "INFERRED call edge should be confirmed")

	// Verify the edge was upgraded in place.
	edges := g.GetOutEdges("pkg/b/b.go::Caller")
	require.NotEmpty(t, edges)
	var confirmed *graph.Edge
	for _, e := range edges {
		if e.To == "pkg/a/a.go::Hello" && e.Kind == graph.EdgeCalls {
			confirmed = e
			break
		}
	}
	require.NotNil(t, confirmed, "expected the call edge to still exist")
	assert.Equal(t, 1.0, confirmed.Confidence)
	assert.Equal(t, "EXTRACTED", confirmed.ConfidenceLabel)
	assert.Equal(t, graph.OriginLSPResolved, confirmed.Origin,
		"call edges resolved by go/types should land at lsp_resolved")
	require.NotNil(t, confirmed.Meta)
	assert.Equal(t, "go-types", confirmed.Meta["semantic_source"])
}

func TestGoAnalysis_AddsMissingCallEdge(t *testing.T) {
	root := resolvedTempDir(t)
	twoPackageCallerFixture(t, root)

	g := graph.New()
	g.AddNode(&graph.Node{
		ID: "pkg/a/a.go::Hello", Kind: graph.KindFunction, Name: "Hello",
		FilePath: "pkg/a/a.go", StartLine: 3, EndLine: 5, Language: "go",
	})
	g.AddNode(&graph.Node{
		ID: "pkg/b/b.go::Caller", Kind: graph.KindFunction, Name: "Caller",
		FilePath: "pkg/b/b.go", StartLine: 5, EndLine: 7, Language: "go",
	})
	// No pre-seeded edge — the provider must discover the call.

	p := newTestProvider(t)
	result, err := p.Enrich(g, root)
	require.NoError(t, err)
	require.GreaterOrEqual(t, result.EdgesAdded, 1, "missing call edge should be added")

	edges := g.GetOutEdges("pkg/b/b.go::Caller")
	var added *graph.Edge
	for _, e := range edges {
		if e.To == "pkg/a/a.go::Hello" && e.Kind == graph.EdgeCalls {
			added = e
			break
		}
	}
	require.NotNil(t, added, "expected a new call edge to be added")
	assert.Equal(t, 1.0, added.Confidence)
	assert.Equal(t, graph.OriginLSPResolved, added.Origin)
	require.NotNil(t, added.Meta)
	assert.Equal(t, "go-types", added.Meta["semantic_source"])
}

func TestGoAnalysis_DetectsInterfaceImplementation(t *testing.T) {
	root := resolvedTempDir(t)
	writeGoMod(t, root, "example.com/iface")
	writeFile(t, root, "main.go", `package main

type Greeter interface {
	Greet() string
}

type EnglishGreeter struct{}

func (e EnglishGreeter) Greet() string {
	return "hello"
}
`)

	g := graph.New()
	g.AddNode(&graph.Node{
		ID: "main.go::Greeter", Kind: graph.KindInterface, Name: "Greeter",
		FilePath: "main.go", StartLine: 3, EndLine: 5, Language: "go",
	})
	g.AddNode(&graph.Node{
		ID: "main.go::EnglishGreeter", Kind: graph.KindType, Name: "EnglishGreeter",
		FilePath: "main.go", StartLine: 7, EndLine: 7, Language: "go",
	})
	g.AddNode(&graph.Node{
		ID: "main.go::EnglishGreeter.Greet", Kind: graph.KindMethod, Name: "Greet",
		FilePath: "main.go", StartLine: 9, EndLine: 11, Language: "go",
	})

	p := newTestProvider(t)
	result, err := p.Enrich(g, root)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, result.EdgesAdded, 1, "implements edge should be added")

	edges := g.GetOutEdges("main.go::EnglishGreeter")
	var impl *graph.Edge
	for _, e := range edges {
		if e.To == "main.go::Greeter" && e.Kind == graph.EdgeImplements {
			impl = e
			break
		}
	}
	require.NotNil(t, impl, "expected EdgeImplements from EnglishGreeter to Greeter")
	assert.Equal(t, 1.0, impl.Confidence)
	assert.Equal(t, graph.OriginLSPDispatch, impl.Origin,
		"implements edges should land at lsp_dispatch (one step from literal target)")
}

func TestGoAnalysis_NoFalseImplementsForUnrelatedTypes(t *testing.T) {
	root := resolvedTempDir(t)
	writeGoMod(t, root, "example.com/unrelated")
	writeFile(t, root, "main.go", `package main

type Reader interface {
	Read() string
}

type Counter struct{}

func (c Counter) Increment() int {
	return 1
}
`)

	g := graph.New()
	g.AddNode(&graph.Node{
		ID: "main.go::Reader", Kind: graph.KindInterface, Name: "Reader",
		FilePath: "main.go", StartLine: 3, EndLine: 5, Language: "go",
	})
	g.AddNode(&graph.Node{
		ID: "main.go::Counter", Kind: graph.KindType, Name: "Counter",
		FilePath: "main.go", StartLine: 7, EndLine: 7, Language: "go",
	})
	g.AddNode(&graph.Node{
		ID: "main.go::Counter.Increment", Kind: graph.KindMethod, Name: "Increment",
		FilePath: "main.go", StartLine: 9, EndLine: 11, Language: "go",
	})

	p := newTestProvider(t)
	_, err := p.Enrich(g, root)
	require.NoError(t, err)

	for _, e := range g.GetOutEdges("main.go::Counter") {
		if e.Kind == graph.EdgeImplements && e.To == "main.go::Reader" {
			t.Fatalf("Counter does not implement Reader; provider must not synthesize this edge")
		}
	}
}

func TestGoAnalysis_EnrichesNodeMeta(t *testing.T) {
	root := resolvedTempDir(t)
	writeGoMod(t, root, "example.com/meta")
	writeFile(t, root, "main.go", `package main

func F() (int, error) {
	return 0, nil
}
`)

	g := graph.New()
	g.AddNode(&graph.Node{
		ID: "main.go::F", Kind: graph.KindFunction, Name: "F",
		FilePath: "main.go", StartLine: 3, EndLine: 5, Language: "go",
	})

	p := newTestProvider(t)
	result, err := p.Enrich(g, root)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, result.NodesEnriched, 1)

	node := g.GetNode("main.go::F")
	require.NotNil(t, node)
	require.NotNil(t, node.Meta)
	semType, ok := node.Meta["semantic_type"].(string)
	require.True(t, ok, "semantic_type should be populated as a string")
	assert.Contains(t, semType, "func", "semantic_type should describe a function type")

	retType, ok := node.Meta["return_type"].(string)
	require.True(t, ok, "return_type should be populated for funcs")
	assert.Contains(t, retType, "int")
	assert.Contains(t, retType, "error")

	assert.Equal(t, "go-types", node.Meta["semantic_source"])
}
