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

func TestReconcileContractEdgesForFrontierDeletesTopicGroupAndPreservesSiblingBoundary(t *testing.T) {
	scopedGraph, scopedMulti, scopedA := newTopicFrontierFixture(t, true)
	require.Equal(t, 2, scopedMulti.ReconcileContractEdges())
	scopedA.ReplaceFile("a/client.go", nil)
	require.Zero(t, scopedMulti.ReconcileContractEdgesForFrontier(DerivedInvalidationPlan{
		Flags: DerivedInvalidatesContracts,
		Files: []string{"a/client.go"},
		ContractGroups: []ContractGroupFrontier{
			{WorkspaceID: "ws-a", ProjectID: "project", ContractID: "topic::kafka::orders"},
		},
		ContractSymbolIDs: []string{"a-producer", "a-consumer"},
	}))

	referenceGraph, referenceMulti, _ := newTopicFrontierFixture(t, false)
	require.Equal(t, 1, referenceMulti.ReconcileContractEdges())
	assert.Equal(t, topicFrontierSnapshot(referenceGraph), topicFrontierSnapshot(scopedGraph))
	assert.Nil(t, scopedGraph.GetNode("bridge::ws-a::project::topic::kafka::orders"))
	assert.NotNil(t, scopedGraph.GetNode("bridge::ws-b::project::topic::kafka::orders"))
	assert.NotNil(t, scopedGraph.GetNode("topic::kafka::orders"))
}

func newTopicFrontierFixture(t *testing.T, includeAConsumer bool) (*graph.Graph, *MultiIndexer, *contracts.Registry) {
	t.Helper()
	g := graph.New()
	g.AddBatch([]*graph.Node{
		{ID: "topic::kafka::orders", Kind: graph.KindContract, Name: "orders", FilePath: "b/provider.go", RepoPrefix: "repo-b"},
		{ID: "a-producer", Kind: graph.KindFunction, Name: "producerA", FilePath: "a/provider.go", RepoPrefix: "repo-a", WorkspaceID: "ws-a", ProjectID: "project"},
		{ID: "a-consumer", Kind: graph.KindFunction, Name: "consumerA", FilePath: "a/client.go", RepoPrefix: "repo-a", WorkspaceID: "ws-a", ProjectID: "project"},
		{ID: "b-producer", Kind: graph.KindFunction, Name: "producerB", FilePath: "b/provider.go", RepoPrefix: "repo-b", WorkspaceID: "ws-b", ProjectID: "project"},
		{ID: "b-consumer", Kind: graph.KindFunction, Name: "consumerB", FilePath: "b/client.go", RepoPrefix: "repo-b", WorkspaceID: "ws-b", ProjectID: "project"},
	}, nil)

	regA := contracts.NewRegistry()
	regA.Add(topicFrontierContract(contracts.RoleProvider, "a-producer", "a/provider.go", "repo-a", "ws-a"))
	if includeAConsumer {
		regA.Add(topicFrontierContract(contracts.RoleConsumer, "a-consumer", "a/client.go", "repo-a", "ws-a"))
	}
	regB := contracts.NewRegistry()
	regB.Add(topicFrontierContract(contracts.RoleProvider, "b-producer", "b/provider.go", "repo-b", "ws-b"))
	regB.Add(topicFrontierContract(contracts.RoleConsumer, "b-consumer", "b/client.go", "repo-b", "ws-b"))

	logger := zap.NewNop()
	mi := NewMultiIndexer(g, nil, nil, nil, logger)
	mi.indexers["repo-a"] = &Indexer{graph: g, repoPrefix: "repo-a", workspaceID: "ws-a", projectID: "project", contractRegistry: regA, logger: logger}
	mi.indexers["repo-b"] = &Indexer{graph: g, repoPrefix: "repo-b", workspaceID: "ws-b", projectID: "project", contractRegistry: regB, logger: logger}
	return g, mi, regA
}

func topicFrontierContract(role contracts.Role, symbolID, filePath, repo, workspace string) contracts.Contract {
	return contracts.Contract{
		ID: "topic::kafka::orders", Type: contracts.ContractTopic, Role: role,
		SymbolID: symbolID, FilePath: filePath, RepoPrefix: repo,
		WorkspaceID: workspace, ProjectID: "project", Confidence: 1,
		Meta: map[string]any{"broker": "kafka", "topic": "orders"},
	}
}

func topicFrontierSnapshot(g graph.Store) []string {
	var snapshot []string
	for _, source := range []string{"a-consumer", "b-consumer"} {
		for _, edge := range g.GetOutEdges(source) {
			if edge.Kind == graph.EdgeMatches {
				snapshot = append(snapshot, fmt.Sprintf("match:%s:%s", edge.From, edge.To))
			}
		}
	}
	for _, bridgeID := range []string{
		"bridge::ws-a::project::topic::kafka::orders",
		"bridge::ws-b::project::topic::kafka::orders",
	} {
		if g.GetNode(bridgeID) != nil {
			snapshot = append(snapshot, "node:"+bridgeID)
		}
		for _, edge := range g.GetOutEdges(bridgeID) {
			if edge.Kind == graph.EdgeBridges {
				snapshot = append(snapshot, fmt.Sprintf("bridge:%s:%s:%v", bridgeID, edge.To, edge.Meta["side"]))
			}
		}
	}
	for _, edge := range g.GetInEdges("topic::kafka::orders") {
		if edge.Kind == graph.EdgeProducesTopic || edge.Kind == graph.EdgeConsumesTopic {
			snapshot = append(snapshot, fmt.Sprintf("topic:%s:%s:%s", edge.From, edge.To, edge.Kind))
		}
	}
	sort.Strings(snapshot)
	return snapshot
}
