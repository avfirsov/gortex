package tstypes

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

type lookupCountingStore struct {
	graph.Store
	findInRepoCalls int
	findCalls       int
	inEdgeCalls     int
	outEdgeCalls    int
	nodesByIDCalls  int
}

func (s *lookupCountingStore) FindNodesByNameInRepo(name, repoPrefix string) []*graph.Node {
	s.findInRepoCalls++
	return s.Store.FindNodesByNameInRepo(name, repoPrefix)
}

func (s *lookupCountingStore) FindNodesByName(name string) []*graph.Node {
	s.findCalls++
	return s.Store.FindNodesByName(name)
}

func (s *lookupCountingStore) GetInEdges(id string) []*graph.Edge {
	s.inEdgeCalls++
	return s.Store.GetInEdges(id)
}

func (s *lookupCountingStore) GetOutEdges(id string) []*graph.Edge {
	s.outEdgeCalls++
	return s.Store.GetOutEdges(id)
}

func (s *lookupCountingStore) GetNodesByIDs(ids []string) map[string]*graph.Node {
	s.nodesByIDCalls++
	return s.Store.GetNodesByIDs(ids)
}

func TestApplierMemoizesStoreLookups(t *testing.T) {
	base := graph.New()
	typeNode := &graph.Node{
		ID:         "repo/types.ts::Remote",
		Kind:       graph.KindType,
		Name:       "Remote",
		FilePath:   "repo/types.ts",
		Language:   "typescript",
		RepoPrefix: "repo",
	}
	methodNode := &graph.Node{
		ID:         "repo/types.ts::Remote.run",
		Kind:       graph.KindMethod,
		Name:       "run",
		FilePath:   "repo/types.ts",
		Language:   "typescript",
		RepoPrefix: "repo",
	}
	base.AddBatch([]*graph.Node{typeNode, methodNode}, []*graph.Edge{{
		From: methodNode.ID,
		To:   typeNode.ID,
		Kind: graph.EdgeMemberOf,
	}})

	store := &lookupCountingStore{Store: base}
	ap := newApplier(store, TypeScriptSpec(), "test-types")
	idx := &fileIndex{
		facts:   &fileFacts{file: "repo/use.ts", repoPrefix: "repo"},
		imports: map[string]string{},
		types:   map[string]*graph.Node{},
	}

	for i := 0; i < 100; i++ {
		if got := ap.resolveTypeNode(idx, "Remote"); got == nil || got.ID != typeNode.ID {
			t.Fatalf("resolveTypeNode(Remote) = %#v, want %s", got, typeNode.ID)
		}
	}
	if got := store.findInRepoCalls; got != 1 {
		t.Fatalf("FindNodesByNameInRepo calls = %d, want 1", got)
	}

	for i := 0; i < 100; i++ {
		if got := ap.resolveTypeNode(idx, "Missing"); got != nil {
			t.Fatalf("resolveTypeNode(Missing) = %#v, want nil", got)
		}
	}
	if got := store.findInRepoCalls; got != 2 {
		t.Fatalf("FindNodesByNameInRepo calls including cached miss = %d, want 2", got)
	}

	for i := 0; i < 100; i++ {
		if got := ap.methodOn(typeNode, "run", 0, 0); got == nil || got.ID != methodNode.ID {
			t.Fatalf("methodOn(Remote, run) = %#v, want %s", got, methodNode.ID)
		}
	}
	if got := store.inEdgeCalls; got != 1 {
		t.Fatalf("GetInEdges calls = %d, want 1", got)
	}
	if got := store.nodesByIDCalls; got != 1 {
		t.Fatalf("GetNodesByIDs calls = %d, want 1", got)
	}
}

func TestMethodMemoizationKeepsDepthBound(t *testing.T) {
	base := graph.New()
	child := &graph.Node{ID: "repo/types.ts::Child", Kind: graph.KindType, Name: "Child", Language: "typescript", RepoPrefix: "repo"}
	parent := &graph.Node{ID: "repo/types.ts::Parent", Kind: graph.KindType, Name: "Parent", Language: "typescript", RepoPrefix: "repo"}
	method := &graph.Node{ID: "repo/types.ts::Parent.run", Kind: graph.KindMethod, Name: "run", Language: "typescript", RepoPrefix: "repo"}
	base.AddBatch([]*graph.Node{child, parent, method}, []*graph.Edge{
		{From: child.ID, To: parent.ID, Kind: graph.EdgeExtends},
		{From: method.ID, To: parent.ID, Kind: graph.EdgeMemberOf},
	})

	ap := newApplier(base, TypeScriptSpec(), "test-types")
	if got := ap.methodOn(child, "run", 0, extendsWalkDepth); got != nil {
		t.Fatalf("depth-limited method lookup = %#v, want nil", got)
	}
	if got := ap.methodOn(child, "run", 0, 0); got == nil || got.ID != method.ID {
		t.Fatalf("root method lookup after depth-limited miss = %#v, want %s", got, method.ID)
	}
}

func TestLanguageFilesSkipVendoredSources(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"src/app.ts", "node_modules/pkg/index.ts"} {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte("export const value = 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	g := graph.New()
	g.AddBatch([]*graph.Node{
		{ID: "repo/src/app.ts", Kind: graph.KindFile, Name: "app.ts", FilePath: "repo/src/app.ts", Language: "typescript", RepoPrefix: "repo"},
		{ID: "repo/node_modules/pkg/index.ts", Kind: graph.KindFile, Name: "index.ts", FilePath: "repo/node_modules/pkg/index.ts", Language: "typescript", RepoPrefix: "repo"},
	}, nil)

	files := languageFiles(g, TypeScriptSpec(), "repo", root)
	if len(files) != 1 || files[0].node.FilePath != "repo/src/app.ts" {
		t.Fatalf("languageFiles() = %#v, want only repo/src/app.ts", files)
	}
}
