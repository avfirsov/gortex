package indexer

import (
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/graph"
)

type refFactBatchStore struct {
	graph.Store
	facts map[string]graph.RefFact

	fileBatchCalls int
	outBatchCalls  int
	nodeBatchCalls int
	filePointCalls int
	outPointCalls  int
	nodePointCalls int
}

func newRefFactBatchStore(store graph.Store) *refFactBatchStore {
	return &refFactBatchStore{Store: store, facts: make(map[string]graph.RefFact)}
}

func refFactStorageKey(repo string, fact graph.RefFact) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%d", repo, fact.FromID, fact.ToID, fact.Kind, fact.Line)
}

func (s *refFactBatchStore) BulkSetRefFacts(repoPrefix string, facts []graph.RefFact) error {
	for _, fact := range facts {
		fact.RepoPrefix = repoPrefix
		s.facts[refFactStorageKey(repoPrefix, fact)] = fact
	}
	return nil
}

func (s *refFactBatchStore) DeleteRefFactsByFiles(repoPrefix string, files []string) error {
	wanted := make(map[string]struct{}, len(files))
	for _, file := range files {
		wanted[file] = struct{}{}
	}
	for key, fact := range s.facts {
		if fact.RepoPrefix == repoPrefix {
			if _, ok := wanted[fact.FilePath]; ok {
				delete(s.facts, key)
			}
		}
	}
	return nil
}

func (s *refFactBatchStore) GetFileNodesByPaths(paths []string) map[string][]*graph.Node {
	s.fileBatchCalls++
	return s.Store.GetFileNodesByPaths(paths)
}

func (s *refFactBatchStore) GetOutEdgesByNodeIDs(ids []string) map[string][]*graph.Edge {
	s.outBatchCalls++
	return s.Store.GetOutEdgesByNodeIDs(ids)
}

func (s *refFactBatchStore) GetNodesByIDs(ids []string) map[string]*graph.Node {
	s.nodeBatchCalls++
	return s.Store.GetNodesByIDs(ids)
}

func (s *refFactBatchStore) GetFileNodes(path string) []*graph.Node {
	s.filePointCalls++
	return s.Store.GetFileNodes(path)
}

func (s *refFactBatchStore) GetOutEdges(id string) []*graph.Edge {
	s.outPointCalls++
	return s.Store.GetOutEdges(id)
}

func (s *refFactBatchStore) GetNode(id string) *graph.Node {
	s.nodePointCalls++
	return s.Store.GetNode(id)
}

func TestPersistAllRefFactsLegacyFallbackUsesBoundedBatchReads(t *testing.T) {
	base := graph.New()
	base.AddNode(&graph.Node{ID: "repoA::target", Kind: graph.KindFunction, Name: "Target", FilePath: "repoA/target.go", RepoPrefix: "repoA", Language: "go"})
	for i := 0; i < refFactFileBatch*2+1; i++ {
		file := fmt.Sprintf("repoA/f%03d.go", i)
		fileID := file
		callerID := file + "::Caller"
		base.AddBatch([]*graph.Node{
			{ID: fileID, Kind: graph.KindFile, Name: file, FilePath: file, RepoPrefix: "repoA", Language: "go"},
			{ID: callerID, Kind: graph.KindFunction, Name: "Caller", FilePath: file, RepoPrefix: "repoA", Language: "go"},
		}, []*graph.Edge{{
			From: callerID, To: "repoA::target", Kind: graph.EdgeCalls,
			FilePath: file, Line: i + 1, Confidence: 1,
			Meta: map[string]any{"semantic_source": "go-types"},
		}})
	}
	store := newRefFactBatchStore(base)
	idx := &Indexer{graph: store, repoPrefix: "repoA", logger: zap.NewNop()}

	idx.persistAllRefFacts()

	require.Len(t, store.facts, refFactFileBatch*2+1)
	require.Equal(t, 3, store.fileBatchCalls, "65 files are read in fixed 32-file pages")
	require.Equal(t, 3, store.outBatchCalls)
	require.Equal(t, 3, store.nodeBatchCalls)
	require.Zero(t, store.filePointCalls, "no per-file GetFileNodes N+1")
	require.Zero(t, store.outPointCalls, "no per-node GetOutEdges N+1")
	require.Zero(t, store.nodePointCalls, "no per-edge GetNode N+1")
	for _, fact := range store.facts {
		require.Equal(t, "Target", fact.RefName)
		require.Equal(t, "lsp_resolved", fact.Origin)
		require.Equal(t, "lsp", fact.Tier)
	}
}

func TestPersistRefFactsLegacyFallbackDeletesEmptyFilesAndScopesRepos(t *testing.T) {
	base := graph.New()
	store := newRefFactBatchStore(base)
	staleA := graph.RefFact{RepoPrefix: "repoA", FromID: "old-a", ToID: "old-target", Kind: "calls", FilePath: "same.go"}
	staleB := graph.RefFact{RepoPrefix: "repoB", FromID: "old-b", ToID: "keep-target", Kind: "calls", FilePath: "same.go"}
	require.NoError(t, store.BulkSetRefFacts("repoA", []graph.RefFact{staleA}))
	require.NoError(t, store.BulkSetRefFacts("repoB", []graph.RefFact{staleB}))
	idx := &Indexer{graph: store, repoPrefix: "repoA", logger: zap.NewNop()}

	idx.persistRefFactsForFiles([]string{"same.go", "same.go"})

	var repos []string
	for _, fact := range store.facts {
		repos = append(repos, fact.RepoPrefix)
	}
	sort.Strings(repos)
	require.Equal(t, []string{"repoB"}, repos, "empty repoA file is deleted without touching repoB")
	require.Equal(t, 1, store.fileBatchCalls)
	require.Zero(t, store.filePointCalls)
}
