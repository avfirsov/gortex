package graph

import (
	"reflect"
	"testing"
)

func TestGraphRepoContractProjectionsAreRepositoryWorkspaceScoped(t *testing.T) {
	g := New()
	g.AddBatch([]*Node{
		{ID: "a-py", Kind: KindFile, RepoPrefix: "a", WorkspaceID: "ws", FilePath: "a/main.py", Language: "python"},
		{ID: "a-ts", Kind: KindFile, RepoPrefix: "a", WorkspaceID: "ws", FilePath: "a/mount.ts", Language: "typescript"},
		{ID: "a-other-ws", Kind: KindFile, RepoPrefix: "a", WorkspaceID: "other", FilePath: "a/other.py", Language: "python"},
		{ID: "b-py", Kind: KindFile, RepoPrefix: "b", WorkspaceID: "ws", FilePath: "b/main.py", Language: "python"},
		{ID: "a-reader", Kind: KindType, RepoPrefix: "a", WorkspaceID: "ws", FilePath: "a/R.java", Meta: map[string]any{"spring_config_keys": []string{"app.name"}}},
		{ID: "a-no-hint", Kind: KindType, RepoPrefix: "a", WorkspaceID: "ws", FilePath: "a/N.java", Meta: map[string]any{"other": true}},
		{ID: "b-reader", Kind: KindType, RepoPrefix: "b", WorkspaceID: "ws", FilePath: "b/R.java", Meta: map[string]any{"spring_config_keys": []string{"app.name"}}},
	}, nil)

	if got, want := g.RepoFilePaths("a", "ws", []string{"python"}, []string{".ts"}), []string{"a/main.py", "a/mount.ts"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("RepoFilePaths = %#v, want %#v", got, want)
	}
	readers := g.RepoNodesByKindsWithMetaKey("a", "ws", []NodeKind{KindType, KindField}, "spring_config_keys")
	if len(readers) != 1 || readers[0].ID != "a-reader" {
		t.Fatalf("RepoNodesByKindsWithMetaKey = %#v, want a-reader only", readers)
	}
}
