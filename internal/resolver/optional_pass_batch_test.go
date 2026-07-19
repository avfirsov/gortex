package resolver

import (
	"fmt"
	"iter"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

// optionalPassCountingStore makes point-query regressions visible while
// forwarding every operation to a real in-memory graph. It intentionally does
// not implement ScopedProjectionSequencer: file-scoped tests therefore cover
// the shared adapter's GetFileNodesByPaths path, while RepoEdgesByKinds records
// the repository projection selected by that adapter.
type optionalPassCountingStore struct {
	graph.Store

	edgesByKindCalls         int
	getNodeCalls             int
	getNodesByIDsCalls       int
	findNodesByNameCalls     int
	findNodesByNamesCalls    int
	getFileNodesCalls        int
	getFileNodesByPathsCalls int
	getRepoEdgesCalls        int
	repoEdgesByKindsCalls    int
	getOutEdgesByIDsCalls    int
	addNodeCalls             int
	addEdgeCalls             int
	addBatchCalls            int
	reindexEdgesCalls        int

	maxGetNodesByIDs int
	maxNames         int
	maxOutIDs        int
	maxBatchNodes    int
	maxBatchEdges    int
	maxReindexes     int
}

func (s *optionalPassCountingStore) EdgesByKind(kind graph.EdgeKind) iter.Seq[*graph.Edge] {
	s.edgesByKindCalls++
	return s.Store.EdgesByKind(kind)
}

func (s *optionalPassCountingStore) GetNode(id string) *graph.Node {
	s.getNodeCalls++
	return s.Store.GetNode(id)
}

func (s *optionalPassCountingStore) GetNodesByIDs(ids []string) map[string]*graph.Node {
	s.getNodesByIDsCalls++
	s.maxGetNodesByIDs = max(s.maxGetNodesByIDs, len(ids))
	return s.Store.GetNodesByIDs(ids)
}

func (s *optionalPassCountingStore) FindNodesByName(name string) []*graph.Node {
	s.findNodesByNameCalls++
	return s.Store.FindNodesByName(name)
}

func (s *optionalPassCountingStore) FindNodesByNames(names []string) map[string][]*graph.Node {
	s.findNodesByNamesCalls++
	s.maxNames = max(s.maxNames, len(names))
	return s.Store.FindNodesByNames(names)
}

func (s *optionalPassCountingStore) GetFileNodes(filePath string) []*graph.Node {
	s.getFileNodesCalls++
	return s.Store.GetFileNodes(filePath)
}

func (s *optionalPassCountingStore) GetFileNodesByPaths(filePaths []string) map[string][]*graph.Node {
	s.getFileNodesByPathsCalls++
	return s.Store.GetFileNodesByPaths(filePaths)
}

func (s *optionalPassCountingStore) GetRepoEdges(repoPrefix string) []*graph.Edge {
	s.getRepoEdgesCalls++
	return s.Store.GetRepoEdges(repoPrefix)
}

func (s *optionalPassCountingStore) RepoEdgesByKinds(
	repoPrefixes []string,
	kinds []graph.EdgeKind,
) []graph.RepoEdgeRow {
	s.repoEdgesByKindsCalls++
	return graph.ReadRepoEdgesByKinds(s.Store, repoPrefixes, kinds)
}

func (s *optionalPassCountingStore) GetOutEdgesByNodeIDs(ids []string) map[string][]*graph.Edge {
	s.getOutEdgesByIDsCalls++
	s.maxOutIDs = max(s.maxOutIDs, len(ids))
	return s.Store.GetOutEdgesByNodeIDs(ids)
}

func (s *optionalPassCountingStore) AddNode(node *graph.Node) {
	s.addNodeCalls++
	s.Store.AddNode(node)
}

func (s *optionalPassCountingStore) AddEdge(edge *graph.Edge) {
	s.addEdgeCalls++
	s.Store.AddEdge(edge)
}

func (s *optionalPassCountingStore) AddBatch(nodes []*graph.Node, edges []*graph.Edge) {
	s.addBatchCalls++
	s.maxBatchNodes = max(s.maxBatchNodes, len(nodes))
	s.maxBatchEdges = max(s.maxBatchEdges, len(edges))
	s.Store.AddBatch(nodes, edges)
}

func (s *optionalPassCountingStore) ReindexEdges(batch []graph.EdgeReindex) {
	s.reindexEdgesCalls++
	s.maxReindexes = max(s.maxReindexes, len(batch))
	s.Store.ReindexEdges(batch)
}

func TestExternalCallsMultiFileScopeUsesOneBatchRead(t *testing.T) {
	base := graph.New()
	const fileCount = 37
	nodes := make([]*graph.Node, 0, fileCount)
	edges := make([]*graph.Edge, 0, fileCount)
	files := make([]string, 0, fileCount)
	for i := 0; i < fileCount; i++ {
		file := fmt.Sprintf("repo/file-%02d.go", i)
		id := file + "::call"
		files = append(files, file)
		nodes = append(nodes, &graph.Node{
			ID: id, Name: "call", Kind: graph.KindFunction,
			FilePath: file, RepoPrefix: "repo", Language: "go",
		})
		edges = append(edges, &graph.Edge{
			From: id, To: fmt.Sprintf("dep::example.com/pkg-%02d::Call", i),
			Kind: graph.EdgeCalls, FilePath: file, Line: i + 1,
		})
	}
	base.AddBatch(nodes, edges)
	store := &optionalPassCountingStore{Store: base}

	assert.Equal(t, fileCount, SynthesizeExternalCallsForFiles(store, true, files))
	assert.Equal(t, 1, store.getFileNodesByPathsCalls)
	assert.Zero(t, store.getFileNodesCalls, "file scope must not issue one lookup per file")
	assert.Equal(t, 1, store.getOutEdgesByIDsCalls)
	assert.Equal(t, 2, store.getNodesByIDsCalls,
		"one adapter endpoint batch plus one synthetic-node batch")
	assert.Zero(t, store.getNodeCalls)
	assert.Zero(t, store.addNodeCalls)
	assert.Zero(t, store.addEdgeCalls)
	assert.Equal(t, 1, store.addBatchCalls)
	assert.Equal(t, 1, store.reindexEdgesCalls)
	assert.LessOrEqual(t, store.maxBatchNodes, externalCallMutationChunk)
	assert.LessOrEqual(t, store.maxReindexes, externalCallMutationChunk)
}

func TestExternalCallsRepoScaleKeepsMutationBatchesBounded(t *testing.T) {
	base := graph.New()
	const extra = 17
	total := externalCallMutationChunk*2 + extra
	nodes := make([]*graph.Node, 0, total)
	edges := make([]*graph.Edge, 0, total)
	for i := 0; i < total; i++ {
		file := fmt.Sprintf("repo/file-%04d.go", i)
		id := file + "::call"
		nodes = append(nodes, &graph.Node{
			ID: id, Name: "call", Kind: graph.KindFunction,
			FilePath: file, RepoPrefix: "repo", Language: "go",
		})
		edges = append(edges, &graph.Edge{
			From: id, To: fmt.Sprintf("dep::example.com/pkg-%04d::Call", i),
			Kind: graph.EdgeCalls, FilePath: file, Line: 1,
		})
	}
	base.AddBatch(nodes, edges)
	store := &optionalPassCountingStore{Store: base}

	assert.Equal(t, total, SynthesizeExternalCallsForRepos(store, true, map[string]bool{"repo": true}))
	assert.Equal(t, 1, store.repoEdgesByKindsCalls)
	assert.Zero(t, store.getRepoEdgesCalls, "repo scope must use one set projection")
	assert.Equal(t, 4, store.getNodesByIDsCalls,
		"one adapter endpoint batch plus three bounded synthetic-node batches")
	assert.Zero(t, store.getNodeCalls)
	assert.Zero(t, store.addNodeCalls)
	assert.Zero(t, store.addEdgeCalls)
	assert.Equal(t, 3, store.addBatchCalls)
	assert.Equal(t, 3, store.reindexEdgesCalls)
	assert.LessOrEqual(t, store.maxBatchNodes, externalCallMutationChunk)
	assert.LessOrEqual(t, store.maxReindexes, externalCallMutationChunk)

	// External-call synthesis reports the number of edges terminating at a
	// synthetic terminal, but an idempotent rerun performs no writes.
	rerun := &optionalPassCountingStore{Store: base}
	assert.Equal(t, total, SynthesizeExternalCallsForRepos(rerun, true, map[string]bool{"repo": true}))
	assert.Zero(t, rerun.addBatchCalls)
	assert.Zero(t, rerun.reindexEdgesCalls)
}

func TestSpeculativeDispatchScaleUsesBatchedReadsAndWrites(t *testing.T) {
	base := graph.New()
	total := speculativeSiteChunk*2 + 23
	nodes := make([]*graph.Node, 0, total*2)
	edges := make([]*graph.Edge, 0, total)
	for i := 0; i < total; i++ {
		key := fmt.Sprintf("dispatch%04d", i)
		callerID := fmt.Sprintf("callers/c-%04d.go::call", i)
		targetID := fmt.Sprintf("targets/t-%04d.go::T.%s", i, key)
		nodes = append(nodes,
			&graph.Node{
				ID: callerID, Name: "call", Kind: graph.KindFunction,
				FilePath: fmt.Sprintf("callers/c-%04d.go", i), Language: "go",
			},
			&graph.Node{
				ID: targetID, Name: key, Kind: graph.KindMethod,
				FilePath: fmt.Sprintf("targets/t-%04d.go", i), Language: "go",
			},
		)
		edges = append(edges, &graph.Edge{
			From: callerID, To: "unresolved::*", Kind: graph.EdgeCalls,
			FilePath: fmt.Sprintf("callers/c-%04d.go", i), Line: i + 1,
			Meta: map[string]any{"dyn_shape": "computed_member", "dyn_key": key},
		})
	}
	base.AddBatch(nodes, edges)
	store := &optionalPassCountingStore{Store: base}

	assert.Equal(t, total, ResolveSpeculativeDispatch(store, true))
	windows := (total + speculativeSiteChunk - 1) / speculativeSiteChunk
	assert.Equal(t, 1, store.edgesByKindCalls, "dynamic sites require one call-edge scan")
	assert.Equal(t, windows, store.getNodesByIDsCalls)
	assert.Equal(t, windows, store.findNodesByNamesCalls)
	assert.Equal(t, windows, store.getOutEdgesByIDsCalls)
	assert.Zero(t, store.getNodeCalls)
	assert.Zero(t, store.findNodesByNameCalls)
	assert.Zero(t, store.addEdgeCalls)
	assert.Equal(t, windows, store.addBatchCalls)
	assert.LessOrEqual(t, store.maxNames, speculativeSiteChunk)
	assert.LessOrEqual(t, store.maxOutIDs, speculativeSiteChunk)
	assert.LessOrEqual(t, store.maxBatchEdges, speculativeWriteChunk)

	// The counter now reflects actual logical mutations. Existing speculative
	// endpoints are discovered by the same bounded adjacency batches.
	rerun := &optionalPassCountingStore{Store: base}
	assert.Zero(t, ResolveSpeculativeDispatch(rerun, true))
	assert.Zero(t, rerun.addBatchCalls)
	assert.Equal(t, windows, rerun.getOutEdgesByIDsCalls)
}

func TestSpeculativeDispatchBatchedParity(t *testing.T) {
	base := graph.New()
	base.AddBatch([]*graph.Node{
		{ID: "main.go::goCaller", Name: "goCaller", Kind: graph.KindFunction, FilePath: "main.go", Language: "go"},
		{ID: "main.py::pyCaller", Name: "pyCaller", Kind: graph.KindFunction, FilePath: "main.py", Language: "python"},
		{ID: "go.go::T.run", Name: "run", Kind: graph.KindMethod, FilePath: "go.go", Language: "go"},
		{ID: "py.py::run", Name: "run", Kind: graph.KindFunction, FilePath: "py.py", Language: "python"},
	}, []*graph.Edge{
		{From: "main.go::goCaller", To: "unresolved::*", Kind: graph.EdgeCalls, FilePath: "main.go", Line: 10, Meta: map[string]any{"dyn_shape": "computed_member", "dyn_key": "run"}},
		{From: "main.py::pyCaller", To: "unresolved::*", Kind: graph.EdgeCalls, FilePath: "main.py", Line: 20, Meta: map[string]any{"dyn_shape": "getattr", "dyn_key": "run"}},
	})

	require.Equal(t, 2, ResolveSpeculativeDispatch(base, true))
	goEdges := specEdges(base, "main.go::goCaller")
	pyEdges := specEdges(base, "main.py::pyCaller")
	require.Len(t, goEdges, 1)
	require.Len(t, pyEdges, 1)
	assert.Equal(t, "go.go::T.run", goEdges[0].To)
	assert.Equal(t, "py.py::run", pyEdges[0].To)
	assert.Equal(t, "speculative.computed_member", goEdges[0].Meta["via"])
	assert.Equal(t, "speculative.getattr", pyEdges[0].Meta["via"])
	assert.Equal(t, 1, goEdges[0].Meta["candidate_count"])
	assert.Equal(t, 1, pyEdges[0].Meta["candidate_count"])
}
