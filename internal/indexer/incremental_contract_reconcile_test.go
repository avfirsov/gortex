package indexer

import (
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/contracts"
	"github.com/zzet/gortex/internal/graph"
)

func TestReconcileContractEdgesForFrontierMatchesFullReferenceAcrossSharedIDBoundaries(t *testing.T) {
	scopedGraph, scopedMulti, scopedA := newContractFrontierFixture(t, false)
	require.Equal(t, 2, scopedMulti.ReconcileContractEdges())

	newConsumer := frontierContract("shared-contract", contracts.RoleConsumer, "a-consumer-new", "a/client.go", "repo-a", "ws-a", "project")
	scopedA.ReplaceFile("a/client.go", []contracts.Contract{newConsumer})
	scopedGraph.AddNode(&graph.Node{ID: "a-consumer-new", Kind: graph.KindFunction, Name: "newConsumer", FilePath: "a/client.go", RepoPrefix: "repo-a", WorkspaceID: "ws-a", ProjectID: "project"})
	added := scopedMulti.ReconcileContractEdgesForFrontier(DerivedInvalidationPlan{
		Flags: DerivedInvalidatesContracts,
		Files: []string{"a/client.go"},
		ContractGroups: []ContractGroupFrontier{
			{WorkspaceID: "ws-a", ProjectID: "project", ContractID: "shared-contract"},
		},
		ContractSymbolIDs: []string{"a-consumer-old", "a-consumer-new"},
	})
	require.Equal(t, 1, added)

	referenceGraph, referenceMulti, _ := newContractFrontierFixture(t, true)
	require.Equal(t, 2, referenceMulti.ReconcileContractEdges())
	assert.Equal(t, derivedFrontierSnapshot(referenceGraph), derivedFrontierSnapshot(scopedGraph))
	assert.Empty(t, scopedGraph.GetOutEdges("a-consumer-old"))
	assertMatchEdge(t, scopedGraph.GetOutEdges("a-consumer-new"), "a-provider")
	assertMatchEdge(t, scopedGraph.GetOutEdges("b-consumer"), "b-provider")
}

func newContractFrontierFixture(t *testing.T, useNewConsumer bool) (*graph.Graph, *MultiIndexer, *contracts.Registry) {
	t.Helper()
	g := graph.New()
	consumerA := "a-consumer-old"
	if useNewConsumer {
		consumerA = "a-consumer-new"
	}
	g.AddBatch([]*graph.Node{
		{ID: "shared-contract", Kind: graph.KindContract, Name: "shared-contract", FilePath: "b/provider.go", RepoPrefix: "repo-b"},
		{ID: "a-provider", Kind: graph.KindFunction, Name: "providerA", FilePath: "a/provider.go", RepoPrefix: "repo-a", WorkspaceID: "ws-a", ProjectID: "project"},
		{ID: consumerA, Kind: graph.KindFunction, Name: "consumerA", FilePath: "a/client.go", RepoPrefix: "repo-a", WorkspaceID: "ws-a", ProjectID: "project"},
		{ID: "b-provider", Kind: graph.KindFunction, Name: "providerB", FilePath: "b/provider.go", RepoPrefix: "repo-b", WorkspaceID: "ws-b", ProjectID: "project"},
		{ID: "b-consumer", Kind: graph.KindFunction, Name: "consumerB", FilePath: "b/client.go", RepoPrefix: "repo-b", WorkspaceID: "ws-b", ProjectID: "project"},
	}, nil)

	regA := contracts.NewRegistry()
	regA.Add(frontierContract("shared-contract", contracts.RoleProvider, "a-provider", "a/provider.go", "repo-a", "ws-a", "project"))
	regA.Add(frontierContract("shared-contract", contracts.RoleConsumer, consumerA, "a/client.go", "repo-a", "ws-a", "project"))
	regB := contracts.NewRegistry()
	regB.Add(frontierContract("shared-contract", contracts.RoleProvider, "b-provider", "b/provider.go", "repo-b", "ws-b", "project"))
	regB.Add(frontierContract("shared-contract", contracts.RoleConsumer, "b-consumer", "b/client.go", "repo-b", "ws-b", "project"))

	logger := zap.NewNop()
	mi := NewMultiIndexer(g, nil, nil, nil, logger)
	mi.indexers["repo-a"] = &Indexer{graph: g, repoPrefix: "repo-a", workspaceID: "ws-a", projectID: "project", contractRegistry: regA, logger: logger}
	mi.indexers["repo-b"] = &Indexer{graph: g, repoPrefix: "repo-b", workspaceID: "ws-b", projectID: "project", contractRegistry: regB, logger: logger}
	return g, mi, regA
}

func frontierContract(id string, role contracts.Role, symbolID, filePath, repo, workspace, project string) contracts.Contract {
	return contracts.Contract{
		ID: id, Type: contracts.ContractHTTP, Role: role, SymbolID: symbolID,
		FilePath: filePath, RepoPrefix: repo, WorkspaceID: workspace, ProjectID: project,
		Meta: map[string]any{"method": "GET", "path": "/same"}, Confidence: 1,
	}
}

func derivedFrontierSnapshot(g graph.Store) []string {
	var snapshot []string
	for _, source := range []string{"a-consumer-old", "a-consumer-new", "b-consumer"} {
		for _, edge := range g.GetOutEdges(source) {
			if edge.Kind == graph.EdgeMatches {
				snapshot = append(snapshot, fmt.Sprintf("match:%s:%s:%s:%d", edge.From, edge.To, edge.FilePath, edge.Line))
			}
		}
	}
	for _, bridgeID := range []string{
		"bridge::ws-a::project::shared-contract",
		"bridge::ws-b::project::shared-contract",
	} {
		if node := g.GetNode(bridgeID); node != nil {
			snapshot = append(snapshot, "node:"+bridgeID)
		}
		for _, edge := range g.GetOutEdges(bridgeID) {
			if edge.Kind == graph.EdgeBridges {
				snapshot = append(snapshot, fmt.Sprintf("bridge:%s:%s:%v", bridgeID, edge.To, edge.Meta["side"]))
			}
		}
	}
	sort.Strings(snapshot)
	return snapshot
}

func assertMatchEdge(t *testing.T, edges []*graph.Edge, target string) {
	t.Helper()
	var matches []*graph.Edge
	for _, edge := range edges {
		if edge.Kind == graph.EdgeMatches {
			matches = append(matches, edge)
		}
	}
	require.Len(t, matches, 1)
	assert.Equal(t, target, matches[0].To)
}
