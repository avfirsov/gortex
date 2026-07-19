package graph

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReplaceContractOwnersKeepsSharedIDOwnersRepositoryScoped(t *testing.T) {
	g := New()
	g.AddBatch([]*Node{
		{ID: "repo-a::handler", Kind: KindFunction, Name: "handlerA", FilePath: "repo-a/a.go", RepoPrefix: "repo-a"},
		{ID: "repo-b::handler", Kind: KindFunction, Name: "handlerB", FilePath: "repo-b/b.go", RepoPrefix: "repo-b"},
		{ID: "repo-a::service", Kind: KindFunction, Name: "service", FilePath: "repo-a/a.go", RepoPrefix: "repo-a"},
		{ID: "shared-contract", Kind: KindContract, Name: "shared-contract", FilePath: "repo-b/b.go", RepoPrefix: "repo-b"},
		{ID: "di-token", Kind: KindInterface, Name: "di-token", FilePath: "repo-a/a.go", RepoPrefix: "repo-a"},
	}, []*Edge{
		{From: "repo-a::handler", To: "shared-contract", Kind: EdgeProvides, FilePath: "repo-a/a.go", Line: 10},
		{From: "repo-b::handler", To: "shared-contract", Kind: EdgeConsumes, FilePath: "repo-b/b.go", Line: 20},
		{From: "repo-a::service", To: "di-token", Kind: EdgeProvides, FilePath: "repo-a/a.go", Line: 30},
	})

	result, err := ReplaceContractOwners(g, ContractOwnerReplacement{
		RepoPrefix:     "repo-a",
		FilePaths:      []string{"repo-a/a.go"},
		TouchedNodeIDs: []string{"shared-contract"},
		Nodes: []*Node{
			{ID: "shared-contract", Kind: KindContract, Name: "shared-contract", FilePath: "repo-a/a.go", RepoPrefix: "repo-a"},
		},
		Edges: []*Edge{
			{From: "repo-a::handler", To: "shared-contract", Kind: EdgeConsumes, FilePath: "repo-a/a.go", Line: 11},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.EdgesRemoved)
	assertEdgeSet(t, g.GetOutEdges("repo-a::handler"), EdgeConsumes, "shared-contract", 11)
	assertEdgeSet(t, g.GetOutEdges("repo-b::handler"), EdgeConsumes, "shared-contract", 20)
	assertEdgeSet(t, g.GetOutEdges("repo-a::service"), EdgeProvides, "di-token", 30)

	_, err = ReplaceContractOwners(g, ContractOwnerReplacement{
		RepoPrefix:     "repo-a",
		FilePaths:      []string{"repo-a/a.go"},
		TouchedNodeIDs: []string{"shared-contract"},
	})
	require.NoError(t, err)
	assert.NotNil(t, g.GetNode("shared-contract"), "repo B still owns the shared canonical ID")
	assertEdgeSet(t, g.GetOutEdges("repo-b::handler"), EdgeConsumes, "shared-contract", 20)
	assertEdgeSet(t, g.GetOutEdges("repo-a::service"), EdgeProvides, "di-token", 30)

	_, err = ReplaceContractOwners(g, ContractOwnerReplacement{
		RepoPrefix:     "repo-b",
		FilePaths:      []string{"repo-b/b.go"},
		TouchedNodeIDs: []string{"shared-contract"},
	})
	require.NoError(t, err)
	assert.Nil(t, g.GetNode("shared-contract"), "the final owner deletion prunes the contract node")
	assert.Empty(t, g.GetOutEdges("repo-b::handler"))
}

func assertEdgeSet(t *testing.T, edges []*Edge, kind EdgeKind, to string, line int) {
	t.Helper()
	require.Len(t, edges, 1)
	assert.Equal(t, kind, edges[0].Kind)
	assert.Equal(t, to, edges[0].To)
	assert.Equal(t, line, edges[0].Line)
}
