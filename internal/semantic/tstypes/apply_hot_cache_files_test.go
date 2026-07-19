package tstypes

import (
	"iter"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

type fileProjectionCountingStore struct {
	graph.Store
	projectionCalls int
}

func (s *fileProjectionCountingStore) NodesInFilesByKind(files []string, kinds []graph.NodeKind) []*graph.Node {
	s.projectionCalls++
	return s.Store.(graph.NodesInFilesByKindFinder).NodesInFilesByKind(files, kinds)
}

// The per-file node projection was the one store read the hot cache's
// node/name/adjacency funnels never covered: every page applier of all four
// phases re-fetched the same file sets. A second applier sharing the pass
// cache must hydrate the same page without a single store round-trip, see
// the same shared node pointers, and observe cached emptiness for files
// with no matching nodes.
func TestHotCacheDedupesFileProjection(t *testing.T) {
	base := graph.New()
	typeNode := &graph.Node{
		ID:         "repo/a.ts::A",
		Kind:       graph.KindType,
		Name:       "A",
		FilePath:   "repo/a.ts",
		Language:   "typescript",
		RepoPrefix: "repo",
	}
	base.AddBatch([]*graph.Node{typeNode}, nil)

	store := &fileProjectionCountingStore{Store: base}
	hot := newApplyHotCache(1 << 20)
	page := []*fileFacts{
		{file: "repo/a.ts", repoPrefix: "repo"},
		{file: "repo/b.ts", repoPrefix: "repo"},
	}

	first := newApplier(store, TypeScriptSpec(), "test-types").withHotCache(hot)
	first.preload(page)
	if store.projectionCalls != 1 {
		t.Fatalf("first preload projection calls = %d, want 1", store.projectionCalls)
	}
	if first.nodesByID[typeNode.ID] == nil {
		t.Fatal("first applier did not hydrate the file's type node")
	}

	second := newApplier(store, TypeScriptSpec(), "test-types").withHotCache(hot)
	second.preload(page)
	if store.projectionCalls != 1 {
		t.Fatalf("second preload re-fetched the projection: calls = %d, want 1", store.projectionCalls)
	}
	if second.nodesByID[typeNode.ID] == nil {
		t.Fatal("second applier did not see the cached file group")
	}
	if first.nodesByID[typeNode.ID] != second.nodesByID[typeNode.ID] {
		t.Fatal("cached file group broke node-pointer sharing across appliers")
	}
	if group, ok := hot.getFiles("repo/b.ts"); !ok || len(group) != 0 {
		t.Fatalf("empty file not negatively cached: group=%v ok=%v", group, ok)
	}

	// A cold applier without the pass cache must still hydrate correctly.
	uncached := newApplier(store, TypeScriptSpec(), "test-types")
	uncached.preload(page)
	if uncached.nodesByID[typeNode.ID] == nil {
		t.Fatal("cache-less applier did not hydrate the file's type node")
	}
	if store.projectionCalls != 2 {
		t.Fatalf("cache-less preload projection calls = %d, want 2", store.projectionCalls)
	}
}

type fileProjectionStreamingStore struct {
	fileProjectionCountingStore
	seqCalls int
}

func (s *fileProjectionStreamingStore) NodesInFilesByKindSeq(files []string, kinds []graph.NodeKind) iter.Seq2[string, []*graph.Node] {
	s.seqCalls++
	byFile := make(map[string][]*graph.Node)
	finder := s.Store.(graph.NodesInFilesByKindFinder)
	for _, node := range finder.NodesInFilesByKind(files, kinds) {
		byFile[node.FilePath] = append(byFile[node.FilePath], node)
	}
	return func(yield func(string, []*graph.Node) bool) {
		for _, file := range files {
			if group := byFile[file]; len(group) > 0 {
				if !yield(file, group) {
					return
				}
			}
		}
	}
}

// Same dedupe contract through the streaming access path — the branch the
// production SQLite store takes.
func TestHotCacheDedupesFileProjectionStreamer(t *testing.T) {
	base := graph.New()
	typeNode := &graph.Node{
		ID:         "repo/a.ts::A",
		Kind:       graph.KindType,
		Name:       "A",
		FilePath:   "repo/a.ts",
		Language:   "typescript",
		RepoPrefix: "repo",
	}
	base.AddBatch([]*graph.Node{typeNode}, nil)

	store := &fileProjectionStreamingStore{fileProjectionCountingStore: fileProjectionCountingStore{Store: base}}
	hot := newApplyHotCache(1 << 20)
	page := []*fileFacts{
		{file: "repo/a.ts", repoPrefix: "repo"},
		{file: "repo/b.ts", repoPrefix: "repo"},
	}

	first := newApplier(store, TypeScriptSpec(), "test-types").withHotCache(hot)
	first.preload(page)
	if store.seqCalls != 1 {
		t.Fatalf("first preload streamer calls = %d, want 1", store.seqCalls)
	}
	if first.nodesByID[typeNode.ID] == nil {
		t.Fatal("first applier did not hydrate through the streamer")
	}

	second := newApplier(store, TypeScriptSpec(), "test-types").withHotCache(hot)
	second.preload(page)
	if store.seqCalls != 1 {
		t.Fatalf("second preload re-streamed the projection: calls = %d, want 1", store.seqCalls)
	}
	if second.nodesByID[typeNode.ID] == nil {
		t.Fatal("second applier did not see the cached file group")
	}
	if group, ok := hot.getFiles("repo/b.ts"); !ok || len(group) != 0 {
		t.Fatalf("un-yielded file not negatively cached: group=%v ok=%v", group, ok)
	}
}
