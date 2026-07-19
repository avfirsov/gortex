package mcp

import (
	"fmt"
	"iter"
	"sync/atomic"
	"testing"

	"github.com/zzet/gortex/internal/analysis"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/search"
)

// analysisProjectionCountingStore fails the regression at the capability
// boundary: every retained analyzer must use a streaming, kind-scoped
// projection. The legacy whole-corpus methods remain available only because
// graph.Store requires them, and any call is counted as a failure.
type analysisProjectionCountingStore struct {
	*graph.Graph
	allNodes       atomic.Int64
	allEdges       atomic.Int64
	allNodesLight  atomic.Int64
	allEdgesLight  atomic.Int64
	nodeSequences  atomic.Int64
	edgeSequences  atomic.Int64
	emptyEdgeScope atomic.Int64
}

func (s *analysisProjectionCountingStore) AllNodes() []*graph.Node {
	s.allNodes.Add(1)
	return nil
}

func (s *analysisProjectionCountingStore) AllEdges() []*graph.Edge {
	s.allEdges.Add(1)
	return nil
}

func (s *analysisProjectionCountingStore) AllNodesLight() []*graph.Node {
	s.allNodesLight.Add(1)
	return nil
}

func (s *analysisProjectionCountingStore) AllEdgesLight(...graph.EdgeKind) []*graph.Edge {
	s.allEdgesLight.Add(1)
	return nil
}

func (s *analysisProjectionCountingStore) NodesLightSeq() iter.Seq[*graph.Node] {
	s.nodeSequences.Add(1)
	return s.Graph.NodesLightSeq()
}

func (s *analysisProjectionCountingStore) EdgesLightSeq(kinds ...graph.EdgeKind) iter.Seq[*graph.Edge] {
	s.edgeSequences.Add(1)
	if len(kinds) == 0 {
		s.emptyEdgeScope.Add(1)
	}
	return s.Graph.EdgesLightSeq(kinds...)
}

type retainedAnalysisCaches struct {
	communities *analysis.CommunityResult
	partition   *analysis.LeidenPartitionCache
	processes   *analysis.ProcessResult
	pageRank    *analysis.PageRankResult
	adjacency   *analysis.AdjacencySnapshot
	concepts    *search.AutoConcepts
	hits        *analysis.HITSResult
}

func runRetainedAnalysisCaches(store graph.Store) *retainedAnalysisCaches {
	communities, partition, _ := analysis.DetectCommunitiesLeidenIncremental(store, nil)
	return &retainedAnalysisCaches{
		communities: communities,
		partition:   partition,
		processes:   analysis.DiscoverProcesses(store),
		pageRank:    analysis.ComputePageRank(store),
		adjacency:   analysis.BuildAdjacencySnapshot(store),
		concepts:    search.BuildAutoConcepts(store),
		hits:        analysis.ComputeHITS(store),
	}
}

func TestRetainedAnalysisUsesStreamingScopedProjections(t *testing.T) {
	store := &analysisProjectionCountingStore{Graph: analysisProjectionTestGraph(96)}
	got := runRetainedAnalysisCaches(store)
	if got.communities == nil || got.partition == nil || got.processes == nil ||
		got.pageRank == nil || got.adjacency == nil || got.concepts == nil || got.hits == nil {
		t.Fatal("representative analysis returned a nil cache")
	}
	if calls := store.allNodes.Load(); calls != 0 {
		t.Fatalf("AllNodes calls = %d, want 0", calls)
	}
	if calls := store.allEdges.Load(); calls != 0 {
		t.Fatalf("AllEdges calls = %d, want 0", calls)
	}
	if calls := store.allNodesLight.Load(); calls != 0 {
		t.Fatalf("slice-shaped light node scans = %d, want 0", calls)
	}
	if calls := store.allEdgesLight.Load(); calls != 0 {
		t.Fatalf("slice-shaped light edge scans = %d, want 0", calls)
	}
	if store.nodeSequences.Load() == 0 || store.edgeSequences.Load() == 0 {
		t.Fatalf("streaming projections not exercised: nodes=%d edges=%d", store.nodeSequences.Load(), store.edgeSequences.Load())
	}
	if calls := store.emptyEdgeScope.Load(); calls != 0 {
		t.Fatalf("unscoped edge projections = %d, want 0", calls)
	}
}

func analysisProjectionTestGraph(nodeCount int) *graph.Graph {
	g := graph.New()
	nodes := make([]*graph.Node, 0, nodeCount)
	edges := make([]*graph.Edge, 0, nodeCount*2+nodeCount/8)
	ids := make([]string, nodeCount)
	for i := 0; i < nodeCount; i++ {
		id := fmt.Sprintf("repo/pkg%04d/file.go::HandleRequest%05d", i/16, i)
		ids[i] = id
		nodes = append(nodes, &graph.Node{
			ID:         id,
			Name:       fmt.Sprintf("HandleRequest%05d", i),
			QualName:   fmt.Sprintf("pkg%04d.HandleRequest%05d", i/16, i),
			Kind:       graph.KindFunction,
			Language:   "go",
			FilePath:   fmt.Sprintf("repo/pkg%04d/file.go", i/16),
			RepoPrefix: "repo",
		})
	}
	for i := 0; i < nodeCount; i++ {
		edges = append(edges,
			&graph.Edge{From: ids[i], To: ids[(i+1)%nodeCount], Kind: graph.EdgeCalls},
			&graph.Edge{From: ids[i], To: ids[(i+17)%nodeCount], Kind: graph.EdgeReferences},
		)
		if i%8 == 0 {
			edges = append(edges, &graph.Edge{From: ids[i], To: ids[(i+31)%nodeCount], Kind: graph.EdgeImports})
		}
	}
	g.AddBatch(nodes, edges)
	return g
}
