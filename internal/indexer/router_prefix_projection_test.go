package indexer

import (
	"iter"
	"reflect"
	"testing"

	"github.com/zzet/gortex/internal/contracts"
	"github.com/zzet/gortex/internal/graph"
)

func TestRouterPrefixScanFilesUsesScopedFileProjection(t *testing.T) {
	base := graph.New()
	base.AddBatch([]*graph.Node{
		{ID: "a-py", Kind: graph.KindFile, RepoPrefix: "a", WorkspaceID: "ws", FilePath: "a/main.py", Language: "python"},
		{ID: "a-ts", Kind: graph.KindFile, RepoPrefix: "a", WorkspaceID: "ws", FilePath: "a/router.ts", Language: "typescript"},
		{ID: "a-go", Kind: graph.KindFile, RepoPrefix: "a", WorkspaceID: "ws", FilePath: "a/main.go", Language: "go"},
		{ID: "a-other-ws", Kind: graph.KindFile, RepoPrefix: "a", WorkspaceID: "other", FilePath: "a/other.py", Language: "python"},
		{ID: "b-py", Kind: graph.KindFile, RepoPrefix: "b", WorkspaceID: "ws", FilePath: "b/main.py", Language: "python"},
	}, nil)
	counting := &routerProjectionCountingStore{Store: base}
	idx := &Indexer{graph: counting, repoPrefix: "a", workspaceID: "ws"}
	reg := contracts.NewRegistry()
	reg.Add(contracts.Contract{ID: "http::GET::/x", Type: contracts.ContractHTTP, Meta: map[string]any{"framework": "fastapi/flask"}})

	if got, want := idx.routerPrefixScanFiles(reg), []string{"a/main.py", "a/router.ts"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("routerPrefixScanFiles = %#v, want %#v", got, want)
	}
	if counting.projections != 1 || counting.globalFileScans != 0 {
		t.Fatalf("projection calls=%d global scans=%d, want 1 and 0", counting.projections, counting.globalFileScans)
	}

	if got := idx.routerPrefixScanFiles(contracts.NewRegistry()); got != nil {
		t.Fatalf("ineligible registry returned %#v, want nil", got)
	}
	if counting.projections != 1 {
		t.Fatalf("ineligible registry ran projection; calls=%d", counting.projections)
	}
}

type routerProjectionCountingStore struct {
	graph.Store
	projections     int
	globalFileScans int
}

func (s *routerProjectionCountingStore) RepoFilePaths(repoPrefix, workspaceID string, languages, extensions []string) []string {
	s.projections++
	return s.Store.(graph.RepoFilePathReader).RepoFilePaths(repoPrefix, workspaceID, languages, extensions)
}

func (s *routerProjectionCountingStore) NodesByKind(kind graph.NodeKind) iter.Seq[*graph.Node] {
	if kind == graph.KindFile {
		s.globalFileScans++
	}
	return s.Store.NodesByKind(kind)
}
