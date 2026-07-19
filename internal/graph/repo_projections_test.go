package graph

import (
	"reflect"
	"testing"
)

func TestGraphRepoProjectionsAreBatchedExactAndContentFree(t *testing.T) {
	g := New()
	g.AddBatch([]*Node{
		{ID: "a-file", Kind: KindFile, RepoPrefix: "a", FilePath: "a.go", Language: "go"},
		{ID: "a-fn", Kind: KindFunction, RepoPrefix: "a", FilePath: "a.go", Language: "go"},
		{ID: "a-type", Kind: KindType, RepoPrefix: "a", FilePath: "a.go", Language: "go"},
		{ID: "a-content", Kind: KindDoc, RepoPrefix: "a", FilePath: "guide.md", Language: "markdown", Meta: map[string]any{"data_class": "content"}},
		{ID: "b-type", Kind: KindType, RepoPrefix: "b", FilePath: "b.py", Language: "python"},
		{ID: "other-type", Kind: KindType, RepoPrefix: "other", FilePath: "other.rs", Language: "rust"},
		{ID: "empty-iface", Kind: KindInterface, FilePath: "single.go", Language: "go"},
	}, nil)

	wantFiles := []RepoLanguageFileCount{
		{RepoPrefix: "a", FilePath: "a.go", Language: "go", Count: 3},
		{RepoPrefix: "b", FilePath: "b.py", Language: "python", Count: 1},
	}
	if got := g.RepoLanguageFileCounts([]string{"b", "a", "a"}); !reflect.DeepEqual(got, wantFiles) {
		t.Fatalf("file counts = %#v, want %#v", got, wantFiles)
	}

	wantCounts := map[string]map[string]int{
		"a": {"go": 3},
		"b": {"python": 1},
	}
	if got := g.RepoLanguageCounts([]string{"a", "b"}); !reflect.DeepEqual(got, wantCounts) {
		t.Fatalf("language counts = %#v, want %#v", got, wantCounts)
	}

	if got, want := g.RepoNodeIDsByKinds(
		[]string{"b", "a"},
		[]NodeKind{KindInterface, KindType},
	), []string{"a-type", "b-type"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("kind IDs = %#v, want %#v", got, want)
	}
	if got, want := g.RepoNodeIDsByKinds(
		[]string{""},
		[]NodeKind{KindInterface},
	), []string{"empty-iface"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("empty-prefix kind IDs = %#v, want %#v", got, want)
	}
}
