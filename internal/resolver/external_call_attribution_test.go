package resolver

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

type externalMaterializeCountingStore struct {
	graph.Store
	addNodeCalls       int
	addEdgeCalls       int
	addBatchCalls      int
	existingNodeCalls  int
	edgeCandidateCalls int
}

func (s *externalMaterializeCountingStore) AddNode(node *graph.Node) {
	s.addNodeCalls++
	s.Store.AddNode(node)
}

func (s *externalMaterializeCountingStore) AddEdge(edge *graph.Edge) {
	s.addEdgeCalls++
	s.Store.AddEdge(edge)
}

func (s *externalMaterializeCountingStore) AddBatch(nodes []*graph.Node, edges []*graph.Edge) {
	s.addBatchCalls++
	s.Store.AddBatch(nodes, edges)
}

func (s *externalMaterializeCountingStore) ExistingNodeIDs(ids []string) map[string]struct{} {
	s.existingNodeCalls++
	return graph.LookupExistingNodeIDs(s.Store, ids)
}

func (s *externalMaterializeCountingStore) GetEdgeCandidates(endpoints []graph.EdgeEndpoint, sites []graph.EdgeSite) graph.EdgeCandidateSet {
	s.edgeCandidateCalls++
	return s.Store.GetEdgeCandidates(endpoints, sites)
}

func TestAttributeGoExternalCalls_StdlibFunctionMaterialised(t *testing.T) {
	g := graph.New()
	owner := "pkg/foo.go::F"
	g.AddNode(&graph.Node{ID: owner, Kind: graph.KindFunction, Name: "F", FilePath: "pkg/foo.go", Language: "go"})
	// Post-resolveExtern shape: an edge directly to stdlib::fmt::Sprintf.
	edge := &graph.Edge{From: owner, To: "stdlib::fmt::Sprintf", Kind: graph.EdgeCalls, FilePath: "pkg/foo.go", Line: 5}
	g.AddEdge(edge)

	New(g).attributeGoExternalCalls()

	// The symbol becomes a KindFunction with the right metadata.
	sym := g.GetNode("stdlib::fmt::Sprintf")
	require.NotNil(t, sym, "stdlib symbol must be materialised as a node")
	assert.Equal(t, graph.KindFunction, sym.Kind)
	assert.Equal(t, "Sprintf", sym.Name)
	assert.Equal(t, "go", sym.Language)
	assert.Equal(t, true, sym.Meta["external"])
	assert.Equal(t, "fmt", sym.Meta["module_path"])
	assert.Equal(t, "stdlib", sym.Meta["module_role"])

	// And a KindModule parent under module::go:fmt with role=stdlib.
	mod := g.GetNode("module::go:fmt")
	require.NotNil(t, mod, "module parent must be materialised")
	assert.Equal(t, graph.KindModule, mod.Kind)
	assert.Equal(t, "fmt", mod.Name)
	assert.Equal(t, "stdlib", mod.Meta["role"])
	assert.Equal(t, "go", mod.Meta["ecosystem"])

	// EdgeMemberOf: symbol -> module.
	var foundLink bool
	for e := range g.EdgesByKind(graph.EdgeMemberOf) {
		if e.From == "stdlib::fmt::Sprintf" && e.To == "module::go:fmt" {
			foundLink = true
		}
	}
	assert.True(t, foundLink, "symbol must be linked to its module via EdgeMemberOf")
}

func TestAttributeGoExternalCalls_DepUsesFullImportPath(t *testing.T) {
	g := graph.New()
	owner := "pkg/foo.go::F"
	g.AddNode(&graph.Node{ID: owner, Kind: graph.KindFunction, Name: "F", FilePath: "pkg/foo.go", Language: "go"})
	g.AddEdge(&graph.Edge{From: owner, To: "dep::github.com/stretchr/testify/assert::True", Kind: graph.EdgeCalls, FilePath: "pkg/foo.go", Line: 7})

	New(g).attributeGoExternalCalls()

	sym := g.GetNode("dep::github.com/stretchr/testify/assert::True")
	require.NotNil(t, sym)
	assert.Equal(t, "True", sym.Name)
	assert.Equal(t, "github.com/stretchr/testify/assert", sym.Meta["module_path"])
	assert.Equal(t, "dep", sym.Meta["module_role"])

	mod := g.GetNode("module::go:github.com/stretchr/testify/assert")
	require.NotNil(t, mod)
	assert.Equal(t, "assert", mod.Name, "module name must be the last path segment, not the full import path")
	assert.Equal(t, "dep", mod.Meta["role"])
}

func TestAttributeGoExternalCalls_ModuleNodeSharedAcrossSymbols(t *testing.T) {
	g := graph.New()
	owner := "pkg/foo.go::F"
	g.AddNode(&graph.Node{ID: owner, Kind: graph.KindFunction, Name: "F", FilePath: "pkg/foo.go", Language: "go"})
	// Three different functions from the same stdlib package — all
	// should attach to ONE module node, not three.
	for _, sym := range []string{"Marshal", "Unmarshal", "RawMessage"} {
		g.AddEdge(&graph.Edge{
			From: owner, To: "stdlib::encoding/json::" + sym,
			Kind: graph.EdgeCalls, FilePath: "pkg/foo.go", Line: 1,
		})
	}

	New(g).attributeGoExternalCalls()

	count := 0
	for n := range g.NodesByKind(graph.KindModule) {
		if n.ID == "module::go:encoding/json" {
			count++
		}
	}
	assert.Equal(t, 1, count, "exactly one KindModule per import path")
}

func TestAttributeGoExternalCalls_IdempotentOnRerun(t *testing.T) {
	g := graph.New()
	owner := "pkg/foo.go::F"
	g.AddNode(&graph.Node{ID: owner, Kind: graph.KindFunction, Name: "F", FilePath: "pkg/foo.go", Language: "go"})
	g.AddEdge(&graph.Edge{From: owner, To: "stdlib::os::Open", Kind: graph.EdgeCalls, FilePath: "pkg/foo.go", Line: 1})

	r := New(g)
	r.attributeGoExternalCalls()
	r.attributeGoExternalCalls() // second run must not duplicate

	syms := 0
	for n := range g.NodesByKind(graph.KindFunction) {
		if n.ID == "stdlib::os::Open" {
			syms++
		}
	}
	assert.Equal(t, 1, syms, "second pass must not duplicate the symbol node")

	memberEdges := 0
	for e := range g.EdgesByKind(graph.EdgeMemberOf) {
		if e.From == "stdlib::os::Open" && e.To == "module::go:os" {
			memberEdges++
		}
	}
	assert.Equal(t, 1, memberEdges, "second pass must not duplicate the membership edge")
}

func TestMaterializeGoExternalSeenSkipsPackageOnlySymbol(t *testing.T) {
	g := graph.New()
	New(g).materializeGoExternalSeen(map[extKey]struct{}{
		{prefix: "external::", importPath: "os"}: {},
	})

	assert.NotNil(t, g.GetNode("module::go:os"), "package-only target must materialise its module")
	assert.Nil(t, g.GetNode("external::os::"), "package-only target must not create an empty-name function")
	assert.Equal(t, 0, g.EdgeCount(), "package-only target must not create a malformed membership edge")
}

func TestMaterializedGoExternalNodesCarryOwningRepo(t *testing.T) {
	g := graph.New()
	New(g).materializeGoExternalSeen(map[extKey]struct{}{
		{repoPrefix: "repo", prefix: "stdlib::", importPath: "fmt", symbol: "Sprintf"}:       {},
		{repoPrefix: "repo", prefix: "dep::", importPath: "example.com/dep", symbol: "Call"}: {},
	})

	assert.Equal(t, "repo", g.GetNode("repo::module::go:fmt").RepoPrefix)
	assert.Equal(t, "repo", g.GetNode("repo::stdlib::fmt::Sprintf").RepoPrefix)
	assert.Equal(t, "repo", g.GetNode("repo::module::go:example.com/dep").RepoPrefix)
	assert.Empty(t, g.GetNode("dep::example.com/dep::Call").RepoPrefix, "shared legacy dependency symbol remains global")
}

func TestAttributeGoExternalCalls_NonExternEdgesIgnored(t *testing.T) {
	g := graph.New()
	owner := "pkg/foo.go::F"
	g.AddNode(&graph.Node{ID: owner, Kind: graph.KindFunction, Name: "F", FilePath: "pkg/foo.go", Language: "go"})
	// Real intra-repo call — must not be touched.
	g.AddNode(&graph.Node{ID: "pkg/bar.go::Helper", Kind: graph.KindFunction, Name: "Helper", FilePath: "pkg/bar.go", Language: "go"})
	g.AddEdge(&graph.Edge{From: owner, To: "pkg/bar.go::Helper", Kind: graph.EdgeCalls, FilePath: "pkg/foo.go", Line: 1})
	// And an unresolved bare name — also not in scope for this pass.
	g.AddEdge(&graph.Edge{From: owner, To: "unresolved::doSomething", Kind: graph.EdgeCalls, FilePath: "pkg/foo.go", Line: 2})

	before := []string{}
	for n := range g.NodesByKind(graph.KindModule) {
		before = append(before, n.ID)
	}
	New(g).attributeGoExternalCalls()
	after := []string{}
	for n := range g.NodesByKind(graph.KindModule) {
		after = append(after, n.ID)
	}
	assert.Equal(t, before, after, "no module nodes should be created when there are no extern-prefixed targets")
}

func TestMaterializeGoExternalSeenUsesBoundedBatches(t *testing.T) {
	g := graph.New()
	counting := &externalMaterializeCountingStore{Store: g}
	seen := make(map[extKey]struct{}, 6001)
	for i := 0; i < 6001; i++ {
		seen[extKey{prefix: "stdlib::", importPath: "fmt", symbol: "Symbol" + strconv.Itoa(i)}] = struct{}{}
	}

	New(counting).materializeGoExternalSeen(seen)

	assert.Zero(t, counting.addNodeCalls, "external targets must not commit one node at a time")
	assert.Zero(t, counting.addEdgeCalls, "membership links must not commit one edge at a time")
	assert.Equal(t, 2, counting.addBatchCalls, "6001 symbols plus one module should flush in two bounded batches")
	assert.Equal(t, 1, counting.existingNodeCalls, "node presence must use one lightweight batch API")
	assert.Equal(t, 1, counting.edgeCandidateCalls, "membership presence must use one exact candidate batch")
	assert.Equal(t, 6002, g.NodeCount())
	assert.Equal(t, 6001, g.EdgeCount())

	New(counting).materializeGoExternalSeen(seen)
	assert.Equal(t, 2, counting.addBatchCalls, "warm rerun must not rewrite existing nodes or edges")
	assert.Equal(t, 2, counting.existingNodeCalls)
	assert.Equal(t, 2, counting.edgeCandidateCalls)
}
