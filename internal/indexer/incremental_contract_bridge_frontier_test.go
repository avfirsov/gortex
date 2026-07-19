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

func TestReconcileContractEdgesForFrontierRemovesCanonicalRPCBridgeAfterOwnerEviction(t *testing.T) {
	scopedGraph, scopedMulti, scopedRegistry := newCanonicalRPCFrontierFixture(t, true)
	require.Equal(t, 1, scopedMulti.ReconcileContractEdges())

	bridgeID := bridgeNodeID(bridgeGroupKey{
		workspace: "workspace", project: "project", contractID: "grpc::Greeter::SayHello",
	})
	require.NotNil(t, scopedGraph.GetNode(bridgeID))

	// Structural file replacement evicts the old contract owner node and its
	// incident EdgeBridges before contract extraction refreshes the registry.
	// The bridge itself survives because it is stored in the synthetic bridge file.
	scopedGraph.EvictFile("repo/provider.go")
	scopedRegistry.ReplaceFile("repo/provider.go", nil)
	require.NotNil(t, scopedGraph.GetNode(bridgeID))

	added := scopedMulti.ReconcileContractEdgesForFrontier(DerivedInvalidationPlan{
		Flags: DerivedInvalidatesContracts,
		Files: []string{"repo/provider.go"},
		ContractGroups: []ContractGroupFrontier{
			{WorkspaceID: "workspace", ProjectID: "project", ContractID: "grpc::Greeter"},
		},
		ContractSymbolIDs:     []string{"provider"},
		ContractBridgeNodeIDs: []string{bridgeID},
	})
	require.Zero(t, added)

	referenceGraph, referenceMulti, _ := newCanonicalRPCFrontierFixture(t, false)
	require.Zero(t, referenceMulti.ReconcileContractEdges())
	assert.Equal(t, canonicalRPCDerivedSnapshot(referenceGraph), canonicalRPCDerivedSnapshot(scopedGraph))
	assert.Nil(t, scopedGraph.GetNode(bridgeID))
	assert.Empty(t, scopedGraph.GetOutEdges("consumer"))
}

func TestContractBridgeFrontierCaptureUsesBatchedPriorAdjacency(t *testing.T) {
	g := graph.New()
	contractA := &graph.Node{ID: "contract-a", Kind: graph.KindContract, FilePath: "repo/a.go"}
	contractB := &graph.Node{ID: "contract-b", Kind: graph.KindContract, FilePath: "repo/b.go"}
	bridgeID := "bridge::workspace::project::canonical"
	g.AddBatch([]*graph.Node{
		contractA,
		contractB,
		{ID: bridgeID, Kind: graph.KindContractBridge, FilePath: ContractBridgeFilePath},
	}, []*graph.Edge{
		{From: bridgeID, To: contractA.ID, Kind: graph.EdgeBridges, FilePath: ContractBridgeFilePath},
		{From: bridgeID, To: contractB.ID, Kind: graph.EdgeBridges, FilePath: ContractBridgeFilePath},
	})

	store := &incrementalBatchCountingStore{Store: g}
	stages := []*incrementalBatchStage{
		{graphPath: contractA.FilePath, priorNodes: []*graph.Node{contractA}},
		{graphPath: contractB.FilePath, priorNodes: []*graph.Node{contractB}},
	}
	view := loadIncrementalPriorView(store, stages)
	require.Equal(t, []string{bridgeID}, contractBridgeNodeIDsFromPriorView(stages, view))
	assert.Equal(t, int64(1), store.getInEdgesByNodeIDs.Load())
	assert.Equal(t, int64(1), store.getOutEdgesByNodeIDs.Load())
	assert.Zero(t, store.getInEdges.Load())
	assert.Zero(t, store.getOutEdges.Load())

	store.resetCounts()
	require.Equal(t, []string{bridgeID}, contractBridgeNodeIDsForNodes(store, []*graph.Node{contractA, contractB}))
	assert.Equal(t, int64(1), store.getInEdgesByNodeIDs.Load())
	assert.Zero(t, store.getOutEdgesByNodeIDs.Load())
	assert.Zero(t, store.getInEdges.Load())
	assert.Zero(t, store.getOutEdges.Load())
}

func newCanonicalRPCFrontierFixture(t *testing.T, includeProvider bool) (*graph.Graph, *MultiIndexer, *contracts.Registry) {
	t.Helper()
	g := graph.New()
	nodes := []*graph.Node{
		{ID: "grpc::Greeter::SayHello", Kind: graph.KindContract, Name: "Greeter.SayHello", FilePath: "repo/client.go", RepoPrefix: "repo", WorkspaceID: "workspace", ProjectID: "project"},
		{ID: "consumer", Kind: graph.KindFunction, Name: "consumer", FilePath: "repo/client.go", RepoPrefix: "repo", WorkspaceID: "workspace", ProjectID: "project"},
	}
	if includeProvider {
		nodes = append(nodes,
			&graph.Node{ID: "grpc::Greeter", Kind: graph.KindContract, Name: "Greeter", FilePath: "repo/provider.go", RepoPrefix: "repo", WorkspaceID: "workspace", ProjectID: "project"},
			&graph.Node{ID: "provider", Kind: graph.KindMethod, Name: "provider", FilePath: "repo/provider.go", RepoPrefix: "repo", WorkspaceID: "workspace", ProjectID: "project"},
		)
	}
	g.AddBatch(nodes, nil)

	registry := contracts.NewRegistry()
	if includeProvider {
		registry.Add(canonicalRPCContract("grpc::Greeter", contracts.RoleProvider, "provider", "repo/provider.go", ""))
	}
	registry.Add(canonicalRPCContract("grpc::Greeter::SayHello", contracts.RoleConsumer, "consumer", "repo/client.go", "SayHello"))

	logger := zap.NewNop()
	mi := NewMultiIndexer(g, nil, nil, nil, logger)
	mi.indexers["repo"] = &Indexer{
		graph: g, repoPrefix: "repo", workspaceID: "workspace", projectID: "project",
		contractRegistry: registry, logger: logger,
	}
	return g, mi, registry
}

func canonicalRPCContract(id string, role contracts.Role, symbolID, filePath, method string) contracts.Contract {
	return contracts.Contract{
		ID: id, Type: contracts.ContractGRPC, Role: role, SymbolID: symbolID,
		FilePath: filePath, RepoPrefix: "repo", WorkspaceID: "workspace", ProjectID: "project",
		Meta: map[string]any{"service": "Greeter", "method": method}, Confidence: 1,
	}
}

func canonicalRPCDerivedSnapshot(g graph.Store) []string {
	bridgeID := bridgeNodeID(bridgeGroupKey{
		workspace: "workspace", project: "project", contractID: "grpc::Greeter::SayHello",
	})
	var snapshot []string
	if g.GetNode(bridgeID) != nil {
		snapshot = append(snapshot, "node:"+bridgeID)
	}
	for _, source := range []string{"consumer", "provider", bridgeID} {
		for _, edge := range g.GetOutEdges(source) {
			switch edge.Kind {
			case graph.EdgeMatches:
				snapshot = append(snapshot, fmt.Sprintf("match:%s:%s", edge.From, edge.To))
			case graph.EdgeBridges:
				snapshot = append(snapshot, fmt.Sprintf("bridge:%s:%s:%v", edge.From, edge.To, edge.Meta["side"]))
			}
		}
	}
	sort.Strings(snapshot)
	return snapshot
}
