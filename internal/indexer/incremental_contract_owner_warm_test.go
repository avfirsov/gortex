package indexer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/contracts"
	"github.com/zzet/gortex/internal/graph"
)

func TestEnsureIncrementalContractRegistryReloadsSharedIDFromOwnerEdges(t *testing.T) {
	g := graph.New()
	ownerA := contracts.Contract{
		ID: "shared-contract", Type: contracts.ContractHTTP, Role: contracts.RoleProvider,
		SymbolID: "repo-a::handler", FilePath: "repo-a/a.go", RepoPrefix: "repo-a",
		WorkspaceID: "workspace-a", ProjectID: "project-a", Confidence: 0.8,
		Meta: map[string]any{"method": "GET", "path": "/a"},
	}
	ownerB := contracts.Contract{
		ID: "shared-contract", Type: contracts.ContractGRPC, Role: contracts.RoleConsumer,
		SymbolID: "repo-b::client", FilePath: "repo-b/b.go", RepoPrefix: "repo-b",
		WorkspaceID: "workspace-b", ProjectID: "project-b", Confidence: 0.9,
		Meta: map[string]any{"service": "Users", "method": "Get"},
	}
	g.AddBatch([]*graph.Node{
		{ID: ownerA.SymbolID, Kind: graph.KindFunction, Name: "handler", FilePath: ownerA.FilePath, RepoPrefix: ownerA.RepoPrefix},
		{ID: ownerB.SymbolID, Kind: graph.KindFunction, Name: "client", FilePath: ownerB.FilePath, RepoPrefix: ownerB.RepoPrefix},
		// The shared canonical node reflects the last writer (repo B). Repo A
		// must still reload from its owner edge on a warm restart.
		{ID: ownerB.ID, Kind: graph.KindContract, Name: ownerB.ID, FilePath: ownerB.FilePath, RepoPrefix: ownerB.RepoPrefix, WorkspaceID: ownerB.WorkspaceID, ProjectID: ownerB.ProjectID,
			Meta: map[string]any{"type": string(ownerB.Type), "role": string(ownerB.Role), "contract_meta": ownerB.Meta, "confidence": ownerB.Confidence}},
	}, []*graph.Edge{
		{From: ownerA.SymbolID, To: ownerA.ID, Kind: graph.EdgeProvides, FilePath: ownerA.FilePath, Meta: contractOwnerEdgeMeta(ownerA)},
		{From: ownerB.SymbolID, To: ownerB.ID, Kind: graph.EdgeConsumes, FilePath: ownerB.FilePath, Meta: contractOwnerEdgeMeta(ownerB)},
	})

	idxA := &Indexer{graph: g, repoPrefix: "repo-a", workspaceID: "workspace-a", projectID: "project-a", logger: zap.NewNop()}
	idxB := &Indexer{graph: g, repoPrefix: "repo-b", workspaceID: "workspace-b", projectID: "project-b", logger: zap.NewNop()}
	loadedA := idxA.ensureIncrementalContractRegistry().All()
	loadedB := idxB.ensureIncrementalContractRegistry().All()
	require.Len(t, loadedA, 1)
	require.Len(t, loadedB, 1)
	assert.Equal(t, ownerA.SymbolID, loadedA[0].SymbolID)
	assert.Equal(t, ownerA.RepoPrefix, loadedA[0].RepoPrefix)
	assert.Equal(t, ownerA.WorkspaceID, loadedA[0].WorkspaceID)
	assert.Equal(t, ownerA.ProjectID, loadedA[0].ProjectID)
	assert.Equal(t, ownerA.Type, loadedA[0].Type)
	assert.Equal(t, "/a", loadedA[0].Meta["path"])
	assert.Equal(t, ownerB.SymbolID, loadedB[0].SymbolID)
	assert.Equal(t, ownerB.Type, loadedB[0].Type)
}
