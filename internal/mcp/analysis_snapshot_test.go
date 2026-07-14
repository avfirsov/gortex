package mcp

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/zzet/gortex/internal/analysis"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/search"
)

// freshCountingLightStore models the SQLite lightweight scanners: each scan
// materializes fresh nodes, edges, and string backing arrays. The counters make
// backend round trips observable without coupling these tests to SQLite.
type freshCountingLightStore struct {
	graph.Store
	nodeScans atomic.Int64
	edgeScans atomic.Int64
}

func (s *freshCountingLightStore) AllNodesLight() []*graph.Node {
	s.nodeScans.Add(1)
	nodes := graph.AllNodesLight(s.Store)
	out := make([]*graph.Node, 0, len(nodes))
	for _, node := range nodes {
		if node == nil {
			out = append(out, nil)
			continue
		}
		clone := *node
		clone.ID = strings.Clone(node.ID)
		clone.Name = strings.Clone(node.Name)
		clone.QualName = strings.Clone(node.QualName)
		clone.FilePath = strings.Clone(node.FilePath)
		clone.Language = strings.Clone(node.Language)
		clone.RepoPrefix = strings.Clone(node.RepoPrefix)
		clone.Origin = strings.Clone(node.Origin)
		clone.Meta = nil
		out = append(out, &clone)
	}
	return out
}

func (s *freshCountingLightStore) AllEdgesLight(kinds ...graph.EdgeKind) []*graph.Edge {
	s.edgeScans.Add(1)
	edges := graph.EdgesForKindsLight(s.Store, kinds...)
	out := make([]*graph.Edge, 0, len(edges))
	for _, edge := range edges {
		if edge == nil {
			out = append(out, nil)
			continue
		}
		clone := *edge
		clone.From = strings.Clone(edge.From)
		clone.To = strings.Clone(edge.To)
		clone.Kind = graph.EdgeKind(strings.Clone(string(edge.Kind)))
		clone.Origin = strings.Clone(edge.Origin)
		clone.Meta = nil
		out = append(out, &clone)
	}
	return out
}

func TestAnalysisSnapshotCoalescesRepresentativeAnalyzers(t *testing.T) {
	base := analysisSnapshotTestGraph(96, false)
	backing := &freshCountingLightStore{Store: base}
	snapshot := newAnalysisSnapshotStore(backing)

	got := runRetainedAnalysisCaches(snapshot)
	if got.communities == nil || got.pageRank == nil || got.adjacency == nil || got.concepts == nil || got.hits == nil {
		t.Fatal("representative analysis returned a nil cache")
	}
	if scans := backing.nodeScans.Load(); scans != 1 {
		t.Fatalf("light node scans = %d, want 1", scans)
	}
	if scans := backing.edgeScans.Load(); scans != 1 {
		t.Fatalf("light edge scans = %d, want 1", scans)
	}
}

func TestAnalysisSnapshotFiltersEdgesWithoutRescanning(t *testing.T) {
	base := analysisSnapshotTestGraph(32, false)
	backing := &freshCountingLightStore{Store: base}
	snapshot := newAnalysisSnapshotStore(backing)

	all := snapshot.AllEdgesLight()
	calls := snapshot.AllEdgesLight(graph.EdgeCalls)
	references := snapshot.AllEdgesLight(graph.EdgeReferences)
	both := snapshot.AllEdgesLight(graph.EdgeReferences, graph.EdgeCalls)
	if len(all) != base.EdgeCount() {
		t.Fatalf("all edges = %d, want %d", len(all), base.EdgeCount())
	}
	if len(calls) != 32 {
		t.Fatalf("call edges = %d, want 32", len(calls))
	}
	if len(references) != 32 {
		t.Fatalf("reference edges = %d, want 32", len(references))
	}
	if len(both) != len(calls)+len(references) {
		t.Fatalf("combined edges = %d, want %d", len(both), len(calls)+len(references))
	}
	for _, edge := range calls {
		if edge.Kind != graph.EdgeCalls {
			t.Fatalf("call filter returned %q", edge.Kind)
		}
	}
	if got := snapshot.AllEdgesLight(""); got != nil {
		t.Fatalf("empty kind filter returned %d edges, want nil", len(got))
	}
	if scans := backing.edgeScans.Load(); scans != 1 {
		t.Fatalf("light edge scans = %d, want 1", scans)
	}

	first := snapshot.AllNodesLight()
	second := snapshot.AllNodesLight()
	if len(first) == 0 || first[0] != second[0] {
		t.Fatal("node snapshot did not reuse materialized node pointers")
	}
	if scans := backing.nodeScans.Load(); scans != 1 {
		t.Fatalf("light node scans = %d, want 1", scans)
	}
}

func TestAnalysisSnapshotConcurrentReaders(t *testing.T) {
	base := analysisSnapshotTestGraph(128, false)
	backing := &freshCountingLightStore{Store: base}
	snapshot := newAnalysisSnapshotStore(backing)

	const readers = 32
	start := make(chan struct{})
	errCh := make(chan string, readers)
	var wg sync.WaitGroup
	wg.Add(readers)
	for i := 0; i < readers; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			if len(snapshot.AllNodesLight()) != 128 {
				errCh <- "unexpected node count"
				return
			}
			kind := graph.EdgeCalls
			if i%2 == 1 {
				kind = graph.EdgeReferences
			}
			if len(snapshot.AllEdgesLight(kind)) != 128 {
				errCh <- "unexpected filtered edge count"
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
	if scans := backing.nodeScans.Load(); scans != 1 {
		t.Fatalf("concurrent light node scans = %d, want 1", scans)
	}
	if scans := backing.edgeScans.Load(); scans != 1 {
		t.Fatalf("concurrent light edge scans = %d, want 1", scans)
	}
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

func analysisSnapshotTestGraph(nodeCount int, longIDs bool) *graph.Graph {
	g := graph.New()
	nodes := make([]*graph.Node, 0, nodeCount)
	edges := make([]*graph.Edge, 0, nodeCount*2+nodeCount/8)
	ids := make([]string, nodeCount)
	padding := ""
	if longIDs {
		padding = strings.Repeat("deep-component-segment-", 6)
	}
	for i := 0; i < nodeCount; i++ {
		id := fmt.Sprintf("repo/%spkg%04d/file.go::HandleRequest%05d", padding, i/16, i)
		ids[i] = id
		nodes = append(nodes, &graph.Node{
			ID:         id,
			Name:       fmt.Sprintf("HandleRequest%05d", i),
			QualName:   fmt.Sprintf("pkg%04d.HandleRequest%05d", i/16, i),
			Kind:       graph.KindFunction,
			Language:   "go",
			FilePath:   fmt.Sprintf("repo/%spkg%04d/file.go", padding, i/16),
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

var analysisSnapshotBenchmarkSink *retainedAnalysisCaches

// BenchmarkAnalysisSnapshotRetainedHeap models the SQLite backend's fresh
// string materialization and reports the heap still live after the analyzer
// outputs are retained and a full GC completes. Run with -benchtime=1x so the
// retained-B/op metric represents one complete analysis epoch.
func BenchmarkAnalysisSnapshotRetainedHeap(b *testing.B) {
	base := analysisSnapshotTestGraph(6000, true)
	b.Run("baseline", func(b *testing.B) {
		benchmarkAnalysisSnapshotRetainedHeap(b, &freshCountingLightStore{Store: base}, false)
	})
	b.Run("snapshot", func(b *testing.B) {
		benchmarkAnalysisSnapshotRetainedHeap(b, &freshCountingLightStore{Store: base}, true)
	})
}

func benchmarkAnalysisSnapshotRetainedHeap(b *testing.B, backing *freshCountingLightStore, snapshot bool) {
	if b.N != 1 {
		b.Skip("retained heap metric requires -benchtime=1x")
	}
	analysisSnapshotBenchmarkSink = nil
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	store := graph.Store(backing)
	if snapshot {
		store = newAnalysisSnapshotStore(backing)
	}
	b.ReportAllocs()
	b.ResetTimer()
	result := runRetainedAnalysisCaches(store)
	b.StopTimer()

	analysisSnapshotBenchmarkSink = result
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	retained := uint64(0)
	if after.HeapAlloc > before.HeapAlloc {
		retained = after.HeapAlloc - before.HeapAlloc
	}
	b.ReportMetric(float64(retained), "retained-B/op")
	runtime.KeepAlive(result)
}
