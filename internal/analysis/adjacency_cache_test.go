package analysis

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// buildEdgeGraph constructs a small in-memory graph from (from,to) call
// edges. Node IDs follow the "<pkg>/file.go::Sym" shape so packageOfID
// resolves a meaningful package directory.
func buildEdgeGraph(t *testing.T, edges [][2]string) graph.Store {
	t.Helper()
	g := graph.New()
	seen := map[string]bool{}
	add := func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		path := id
		if i := indexOfSep(id); i >= 0 {
			path = id[:i]
		}
		g.AddNode(&graph.Node{ID: id, Kind: graph.KindFunction, Name: id, FilePath: path})
	}
	for _, e := range edges {
		add(e[0])
		add(e[1])
	}
	for _, e := range edges {
		g.AddEdge(&graph.Edge{From: e[0], To: e[1], Kind: graph.EdgeCalls})
	}
	return g
}

func indexOfSep(s string) int {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == ':' && s[i+1] == ':' {
			return i
		}
	}
	return -1
}

func TestWalkCacheKeyDeterministic(t *testing.T) {
	g := buildEdgeGraph(t, [][2]string{
		{"pkg/a.go::A", "pkg/b.go::B"},
		{"pkg/b.go::B", "pkg/c.go::C"},
	})
	s1 := BuildAdjacencySnapshot(g)
	s2 := BuildAdjacencySnapshot(g)
	k1 := s1.WalkCacheKey([]string{"pkg/a.go::A"}, 0)
	k2 := s2.WalkCacheKey([]string{"pkg/a.go::A"}, 0)
	if k1 == "" {
		t.Fatal("expected non-empty key")
	}
	if k1 != k2 {
		t.Fatalf("key not deterministic across identical snapshots: %q vs %q", k1, k2)
	}
	// Seed order must not matter (sorted internally).
	if got := s1.WalkCacheKey([]string{"pkg/a.go::A", "pkg/b.go::B"}, 0); got == k1 {
		t.Fatal("different seed set should produce a different key")
	}
}

func TestWalkCacheKeyPackageInvalidation(t *testing.T) {
	base := [][2]string{
		{"pkg/a.go::A", "pkg/b.go::B"},
		{"other/x.go::X", "other/y.go::Y"},
	}
	g1 := buildEdgeGraph(t, base)
	s1 := BuildAdjacencySnapshot(g1)
	seeds := []string{"other/x.go::X"}
	k1 := s1.WalkCacheKey(seeds, 0)

	// Change a package UNRELATED to the seed's dependency set (pkg/*).
	// The seed walk (other/*) must keep the same key — the incremental
	// property: an edit to pkg/ doesn't bust an other/ walk.
	g2 := buildEdgeGraph(t, append([][2]string{
		{"pkg/a.go::A", "pkg/b.go::B2new"},
	}, base...))
	s2 := BuildAdjacencySnapshot(g2)
	k2 := s2.WalkCacheKey(seeds, 0)
	if k1 != k2 {
		t.Fatalf("unrelated-package edit must not change the walk key: %q vs %q", k1, k2)
	}

	// Now change the seed's OWN package: the key must change (miss).
	g3 := buildEdgeGraph(t, [][2]string{
		{"pkg/a.go::A", "pkg/b.go::B"},
		{"other/x.go::X", "other/y.go::Ynew"}, // seed's out-edge target changed
	})
	s3 := BuildAdjacencySnapshot(g3)
	k3 := s3.WalkCacheKey(seeds, 0)
	if k1 == k3 {
		t.Fatal("changing the seed's dependency package must change the walk key")
	}
}

func TestPackageRootsPopulated(t *testing.T) {
	g := buildEdgeGraph(t, [][2]string{
		{"pkg/a.go::A", "pkg/b.go::B"},
		{"other/x.go::X", "other/y.go::Y"},
	})
	s := BuildAdjacencySnapshot(g)
	if s.PackageRootCount() < 2 {
		t.Fatalf("expected >=2 package roots, got %d", s.PackageRootCount())
	}
}

func TestPackageOfID(t *testing.T) {
	cases := map[string]string{
		"gortex/internal/mcp/server.go::NewServer": "gortex/internal/mcp",
		"pkg/a.go::A":   "pkg",
		"top.go::Sym":   "",
		"a/b/c/f.go::S": "a/b/c",
	}
	for id, want := range cases {
		if got := packageOfID(id); got != want {
			t.Errorf("packageOfID(%q) = %q, want %q", id, got, want)
		}
	}
}
