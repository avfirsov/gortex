package store_sqlite

import (
	"path/filepath"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func mkFnNode(id, name, file string) *graph.Node {
	return &graph.Node{ID: id, Kind: graph.KindFunction, Name: name, FilePath: file, Language: "go"}
}

// --- unit tests over the cache logic in isolation ---

func TestBundleCache_ServesOnlyValidatedFingerprints(t *testing.T) {
	c := &bundleCache{fingerprints: map[string]uint64{}, entries: map[string]*bundleCacheEntry{}}

	b := graph.SymbolBundle{Node: mkFnNode("pkg/x.go::A", "A", "pkg/x.go")}

	// No fingerprint reported for the package yet -> store is a no-op
	// (conservative: never cache an unvalidated bundle).
	c.store(b)
	if _, ok := c.lookup("pkg/x.go::A"); ok {
		t.Fatal("bundle was cached despite no package fingerprint")
	}

	// Report a fingerprint, then store: now it caches and serves.
	c.refresh(map[string]uint64{"pkg": 100})
	c.store(b)
	if _, ok := c.lookup("pkg/x.go::A"); !ok {
		t.Fatal("bundle should be served once its package fingerprint is known")
	}
}

func TestBundleCache_InvalidatesOnFingerprintChange(t *testing.T) {
	c := &bundleCache{fingerprints: map[string]uint64{}, entries: map[string]*bundleCacheEntry{}}
	c.refresh(map[string]uint64{"pkg": 1})
	c.store(graph.SymbolBundle{Node: mkFnNode("pkg/x.go::A", "A", "pkg/x.go")})

	if _, ok := c.lookup("pkg/x.go::A"); !ok {
		t.Fatal("expected a cache hit on the unchanged fingerprint")
	}

	// Fingerprint changes -> the entry is invalidated.
	c.refresh(map[string]uint64{"pkg": 2})
	if _, ok := c.lookup("pkg/x.go::A"); ok {
		t.Fatal("entry must be dropped when its package fingerprint changes")
	}
}

func TestBundleCache_CrossRepoIsolation(t *testing.T) {
	c := &bundleCache{fingerprints: map[string]uint64{}, entries: map[string]*bundleCacheEntry{}}
	// Two repos with the same inner directory name resolve to DIFFERENT
	// package keys because the stored file paths are repo-prefixed.
	c.refresh(map[string]uint64{
		"repoA/pkg": 10,
		"repoB/pkg": 20,
	})
	c.store(graph.SymbolBundle{Node: mkFnNode("repoA/pkg/x.go::A", "A", "repoA/pkg/x.go")})
	c.store(graph.SymbolBundle{Node: mkFnNode("repoB/pkg/x.go::A", "A", "repoB/pkg/x.go")})

	// Bumping only repoA's fingerprint must not touch repoB's entry.
	c.refresh(map[string]uint64{
		"repoA/pkg": 11,
		"repoB/pkg": 20,
	})
	if _, ok := c.lookup("repoA/pkg/x.go::A"); ok {
		t.Fatal("repoA entry should have been invalidated")
	}
	if _, ok := c.lookup("repoB/pkg/x.go::A"); !ok {
		t.Fatal("repoB entry must survive a repoA-only fingerprint bump")
	}
}

func TestBundlePackageKey(t *testing.T) {
	cases := map[string]string{
		"pkg/sub/x.go": "pkg/sub",
		"x.go":         "",
		"":             "",
		"repo/a/b.go":  "repo/a",
	}
	for in, want := range cases {
		if got := bundlePackageKey(in); got != want {
			t.Errorf("bundlePackageKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- integration tests through the store's SearchSymbolBundles ---

func newBundleTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "b.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func seedBundleStore(t *testing.T, s *Store) {
	t.Helper()
	s.AddNode(mkFnNode("pkg/x.go::A", "AlphaWidget", "pkg/x.go"))
	s.AddNode(mkFnNode("pkg/x.go::B", "BetaWidget", "pkg/x.go"))
	s.AddEdge(&graph.Edge{From: "pkg/x.go::A", To: "pkg/x.go::B", Kind: graph.EdgeCalls, FilePath: "pkg/x.go"})

	items := []graph.SymbolFTSItem{
		{NodeID: "pkg/x.go::A", Tokens: "alpha widget"},
		{NodeID: "pkg/x.go::B", Tokens: "beta widget"},
	}
	if err := s.BulkUpsertSymbolFTS("", items); err != nil {
		t.Fatalf("BulkUpsertSymbolFTS: %v", err)
	}
	if err := s.BuildSymbolIndex(); err != nil {
		t.Fatalf("BuildSymbolIndex: %v", err)
	}
}

func bundleByID(bundles []graph.SymbolBundle) map[string]graph.SymbolBundle {
	out := make(map[string]graph.SymbolBundle, len(bundles))
	for _, b := range bundles {
		if b.Node != nil {
			out[b.Node.ID] = b
		}
	}
	return out
}

func TestSearchSymbolBundles_CacheHitOnUnchangedFingerprint(t *testing.T) {
	s := newBundleTestStore(t)
	seedBundleStore(t, s)

	// Report a fingerprint so the first query populates the cache.
	s.SetBundleFingerprints(map[string]uint64{"pkg": 1})

	first, err := s.SearchSymbolBundles("widget", 10)
	if err != nil {
		t.Fatalf("first SearchSymbolBundles: %v", err)
	}
	got := bundleByID(first)
	if b, ok := got["pkg/x.go::A"]; !ok || len(b.OutEdges) != 1 {
		t.Fatalf("expected A with 1 out-edge on first query, got %+v", got["pkg/x.go::A"])
	}

	// Mutate the graph WITHOUT bumping the fingerprint: add a second
	// out-edge from A. A correct content-addressed cache serves the
	// STALE (1-edge) bundle because the fingerprint is unchanged — proof
	// the bundle came from cache, not a fresh fetch.
	s.AddNode(mkFnNode("pkg/x.go::C", "GammaWidget", "pkg/x.go"))
	s.AddEdge(&graph.Edge{From: "pkg/x.go::A", To: "pkg/x.go::C", Kind: graph.EdgeCalls, FilePath: "pkg/x.go"})

	second, err := s.SearchSymbolBundles("widget", 10)
	if err != nil {
		t.Fatalf("second SearchSymbolBundles: %v", err)
	}
	cached := bundleByID(second)["pkg/x.go::A"]
	if len(cached.OutEdges) != 1 {
		t.Fatalf("expected the cached 1-edge bundle to be served on an unchanged fingerprint, got %d edges",
			len(cached.OutEdges))
	}
}

func TestSearchSymbolBundles_MissAndRecomputeOnFingerprintChange(t *testing.T) {
	s := newBundleTestStore(t)
	seedBundleStore(t, s)
	s.SetBundleFingerprints(map[string]uint64{"pkg": 1})

	if _, err := s.SearchSymbolBundles("widget", 10); err != nil {
		t.Fatalf("warm-up query: %v", err)
	}

	// Add a real out-edge, then bump the package fingerprint to signal
	// the content changed. The next query must recompute and surface the
	// new edge.
	s.AddNode(mkFnNode("pkg/x.go::C", "GammaWidget", "pkg/x.go"))
	s.AddEdge(&graph.Edge{From: "pkg/x.go::A", To: "pkg/x.go::C", Kind: graph.EdgeCalls, FilePath: "pkg/x.go"})
	s.SetBundleFingerprints(map[string]uint64{"pkg": 2})

	after, err := s.SearchSymbolBundles("widget", 10)
	if err != nil {
		t.Fatalf("post-invalidation query: %v", err)
	}
	fresh := bundleByID(after)["pkg/x.go::A"]
	if len(fresh.OutEdges) != 2 {
		t.Fatalf("expected the recomputed 2-edge bundle after a fingerprint bump, got %d edges",
			len(fresh.OutEdges))
	}
}

func TestSearchSymbolBundles_UncachedWithoutFingerprints(t *testing.T) {
	s := newBundleTestStore(t)
	seedBundleStore(t, s)
	// No SetBundleFingerprints call -> the cache stays inert and every
	// query recomputes live. Adding an edge must show up immediately.
	first, err := s.SearchSymbolBundles("widget", 10)
	if err != nil {
		t.Fatalf("first query: %v", err)
	}
	if got := bundleByID(first)["pkg/x.go::A"]; len(got.OutEdges) != 1 {
		t.Fatalf("expected 1 edge live, got %d", len(got.OutEdges))
	}

	s.AddNode(mkFnNode("pkg/x.go::C", "GammaWidget", "pkg/x.go"))
	s.AddEdge(&graph.Edge{From: "pkg/x.go::A", To: "pkg/x.go::C", Kind: graph.EdgeCalls, FilePath: "pkg/x.go"})

	second, err := s.SearchSymbolBundles("widget", 10)
	if err != nil {
		t.Fatalf("second query: %v", err)
	}
	if got := bundleByID(second)["pkg/x.go::A"]; len(got.OutEdges) != 2 {
		t.Fatalf("uncached path must reflect the new edge live, got %d", len(got.OutEdges))
	}
}
