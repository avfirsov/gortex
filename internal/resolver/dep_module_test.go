package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

// addDepNode is a tiny helper to materialise a dep::<module> contract
// node the way GoModExtractor + commitInlinedContractToGraph would.
func addDepNode(t *testing.T, g *graph.Graph, repoPrefix, modulePath string) {
	t.Helper()
	g.AddNode(&graph.Node{
		ID:         "dep::" + modulePath,
		Kind:       graph.KindContract,
		Name:       "dep::" + modulePath,
		FilePath:   repoPrefix + "/go.mod",
		Language:   "contract",
		RepoPrefix: repoPrefix,
	})
}

// Sub-package import: importing a path under a declared module's
// directory should resolve to the dep::<module> contract node.
// This is the original bug — internal/parser/tsitter/sql/sql.go
// imports "github.com/gortexhq/tree-sitter-sql/bindings/go" and
// dep::github.com/gortexhq/tree-sitter-sql had zero incoming edges.
func TestResolveAll_DepBridge_SubPackageImport(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{ID: "repo/internal/x.go", Kind: graph.KindFile, Name: "x.go", FilePath: "repo/internal/x.go", Language: "go", RepoPrefix: "repo"})
	addDepNode(t, g, "repo", "github.com/foo/bar")

	importEdge := &graph.Edge{
		From:     "repo/internal/x.go",
		To:       "unresolved::import::github.com/foo/bar/sub/pkg",
		Kind:     graph.EdgeImports,
		FilePath: "repo/internal/x.go",
		Line:     3,
	}
	g.AddEdge(importEdge)

	r := New(g)
	stats := r.ResolveAll()

	assert.Equal(t, 1, stats.Resolved)
	assert.Equal(t, "dep::github.com/foo/bar", importEdge.To)
}

// Bare import equal to the module path also resolves to the dep node.
func TestResolveAll_DepBridge_BareModuleImport(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{ID: "repo/x.go", Kind: graph.KindFile, Name: "x.go", FilePath: "repo/x.go", Language: "go", RepoPrefix: "repo"})
	addDepNode(t, g, "repo", "github.com/foo/bar")

	e := &graph.Edge{From: "repo/x.go", To: "unresolved::import::github.com/foo/bar", Kind: graph.EdgeImports, FilePath: "repo/x.go", Line: 3}
	g.AddEdge(e)

	r := New(g)
	stats := r.ResolveAll()

	assert.Equal(t, 1, stats.Resolved)
	assert.Equal(t, "dep::github.com/foo/bar", e.To)
}

// When a parent module and a nested module are both declared, the
// longer (more specific) module path must win — otherwise importing
// "github.com/aws/aws-sdk-go-v2/service/s3/types" would attribute to
// the parent aws-sdk-go-v2 dep instead of the s3 sub-module dep.
func TestResolveAll_DepBridge_LongestPrefixWins(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{ID: "repo/x.go", Kind: graph.KindFile, Name: "x.go", FilePath: "repo/x.go", Language: "go", RepoPrefix: "repo"})
	addDepNode(t, g, "repo", "github.com/aws/aws-sdk-go-v2")
	addDepNode(t, g, "repo", "github.com/aws/aws-sdk-go-v2/service/s3")

	e := &graph.Edge{From: "repo/x.go", To: "unresolved::import::github.com/aws/aws-sdk-go-v2/service/s3/types", Kind: graph.EdgeImports, FilePath: "repo/x.go", Line: 3}
	g.AddEdge(e)

	r := New(g)
	stats := r.ResolveAll()

	assert.Equal(t, 1, stats.Resolved)
	assert.Equal(t, "dep::github.com/aws/aws-sdk-go-v2/service/s3", e.To)
}

// A module path is a prefix only when the next character is '/' or
// the strings are equal — `foo/bar` must not satisfy import `foo/barbaz`.
func TestResolveAll_DepBridge_NoFalsePositivePathComponent(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{ID: "repo/x.go", Kind: graph.KindFile, Name: "x.go", FilePath: "repo/x.go", Language: "go", RepoPrefix: "repo"})
	addDepNode(t, g, "repo", "github.com/foo/bar")

	e := &graph.Edge{From: "repo/x.go", To: "unresolved::import::github.com/foo/barbaz", Kind: graph.EdgeImports, FilePath: "repo/x.go", Line: 3}
	g.AddEdge(e)

	r := New(g)
	stats := r.ResolveAll()

	// Should fall through to external::, not match dep::.../bar.
	assert.Equal(t, 0, stats.Resolved)
	assert.Equal(t, 1, stats.External)
	assert.Equal(t, "external::github.com/foo/barbaz", e.To)
}

// A dep declared by repo A's go.mod must not satisfy an import in
// repo B even if the module path matches — each go.mod scopes its
// own dep nodes.
func TestResolveAll_DepBridge_RepoScoped(t *testing.T) {
	g := graph.New()
	// File lives in repoB; dep node lives under repoA.
	g.AddNode(&graph.Node{ID: "repoB/x.go", Kind: graph.KindFile, Name: "x.go", FilePath: "repoB/x.go", Language: "go", RepoPrefix: "repoB"})
	addDepNode(t, g, "repoA", "github.com/foo/bar")

	e := &graph.Edge{From: "repoB/x.go", To: "unresolved::import::github.com/foo/bar", Kind: graph.EdgeImports, FilePath: "repoB/x.go", Line: 3}
	g.AddEdge(e)

	r := New(g)
	stats := r.ResolveAll()

	assert.Equal(t, 0, stats.Resolved)
	assert.Equal(t, 1, stats.External)
	assert.Equal(t, "external::github.com/foo/bar", e.To)
}

// Same coverage on the cross-repo resolver: caller in repoB with a
// dep declared by repoA must not bridge; caller in the dep's own
// repo must.
func TestCrossRepoResolveAll_DepBridge(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{ID: "repoA/x.go", Kind: graph.KindFile, Name: "x.go", FilePath: "repoA/x.go", Language: "go", RepoPrefix: "repoA"})
	g.AddNode(&graph.Node{ID: "repoB/y.go", Kind: graph.KindFile, Name: "y.go", FilePath: "repoB/y.go", Language: "go", RepoPrefix: "repoB"})
	addDepNode(t, g, "repoA", "github.com/foo/bar")

	bridged := &graph.Edge{From: "repoA/x.go", To: "unresolved::import::github.com/foo/bar/sub", Kind: graph.EdgeImports, FilePath: "repoA/x.go", Line: 3}
	g.AddEdge(bridged)
	stranded := &graph.Edge{From: "repoB/y.go", To: "unresolved::import::github.com/foo/bar/sub", Kind: graph.EdgeImports, FilePath: "repoB/y.go", Line: 3}
	g.AddEdge(stranded)

	cr := NewCrossRepo(g)
	stats := cr.ResolveAll()

	require.NotNil(t, stats)
	assert.Equal(t, "dep::github.com/foo/bar", bridged.To, "caller in dep's own repo bridges")
	assert.Equal(t, "external::github.com/foo/bar/sub", stranded.To, "caller in foreign repo stays external")
}
