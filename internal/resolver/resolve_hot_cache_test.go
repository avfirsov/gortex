package resolver

import (
	"fmt"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func hotCacheTestNode(id, name, repo string) *graph.Node {
	return &graph.Node{
		ID:         id,
		Kind:       graph.KindFunction,
		Name:       name,
		FilePath:   repo + "/file.go",
		Language:   "go",
		RepoPrefix: repo,
	}
}

func TestResolveHotCacheRotationBoundsBytes(t *testing.T) {
	budget := int64(64 << 10) // tiny budget forces rotation
	c := newResolveHotCache(budget)
	for i := 0; i < 4096; i++ {
		c.putNode(hotCacheTestNode(fmt.Sprintf("repo::fn::%05d", i), fmt.Sprintf("fn%05d", i), "repo"))
	}
	retained := c.cur.bytes + c.prev.bytes
	// Two generations, each rotated below budget/2 plus one entry of
	// overshoot; anything above 2x budget means rotation is broken.
	if retained > 2*budget {
		t.Fatalf("retained %d bytes exceeds 2x budget %d", retained, budget)
	}
	// The most recently inserted entry must still be resident.
	if _, ok := c.getNode("repo::fn::04095"); !ok {
		t.Fatal("most recent entry evicted by rotation")
	}
}

func TestResolveHotCacheNegativeNameGroupsAreCached(t *testing.T) {
	c := newResolveHotCache(1 << 20)
	key := hotNameKey("repo", "language:go", "definitelyMissing")
	if _, ok := c.getNames(key); ok {
		t.Fatal("unexpected hit before put")
	}
	c.putNames(key, nil)
	hits, ok := c.getNames(key)
	if !ok {
		t.Fatal("cached negative name group not returned")
	}
	if len(hits) != 0 {
		t.Fatalf("cached negative returned %d nodes", len(hits))
	}
}

func TestCachedParallelGetNodesByIDsServesRepeatsFromCache(t *testing.T) {
	g := graph.New()
	n1 := hotCacheTestNode("repo::fn::a", "a", "repo")
	n2 := hotCacheTestNode("repo::fn::b", "b", "repo")
	g.AddNode(n1)
	g.AddNode(n2)

	r := &Resolver{graph: g, hotCache: newResolveHotCache(1 << 20)}
	ids := []string{"repo::fn::a", "repo::fn::b", "repo::fn::missing"}

	first := r.cachedParallelGetNodesByIDs(ids)
	if first["repo::fn::a"] == nil || first["repo::fn::b"] == nil {
		t.Fatalf("first hydration missing nodes: %v", first)
	}
	if _, present := first["repo::fn::missing"]; present {
		t.Fatal("missing id must be absent from the result map")
	}
	if r.hotCache.nodeHits != 0 {
		t.Fatalf("first hydration should be all misses, hits=%d", r.hotCache.nodeHits)
	}

	second := r.cachedParallelGetNodesByIDs(ids)
	if second["repo::fn::a"] == nil || second["repo::fn::b"] == nil {
		t.Fatalf("second hydration missing nodes: %v", second)
	}
	if r.hotCache.nodeHits < 2 {
		t.Fatalf("second hydration should hit the cache, hits=%d", r.hotCache.nodeHits)
	}
	// Negatives are never cached: the missing id must have been re-queried.
	if _, ok := r.hotCache.getNode("repo::fn::missing"); ok {
		t.Fatal("negative node result must not be cached")
	}
}

func TestWarmRepoLanguageNameCacheReusesHotGroupsAcrossPages(t *testing.T) {
	g := graph.New()
	def := hotCacheTestNode("repo::fn::Target", "Target", "repo")
	src := hotCacheTestNode("repo::fn::caller", "caller", "repo")
	g.AddNode(def)
	g.AddNode(src)

	page := []*graph.Edge{{
		From: src.ID,
		To:   "unresolved::Target",
		Kind: graph.EdgeCalls,
	}}

	r := &Resolver{graph: g, hotCache: newResolveHotCache(1 << 20)}
	// Production order: warmLookupCacheWithSources installs the source-node
	// ID cache before the name warm derives repo/language scopes from it.
	r.nodeByID = map[string]*graph.Node{src.ID: src}

	if _, _, err := r.warmRepoLanguageNameCache(page); err != nil {
		t.Fatal(err)
	}
	firstHits := r.nodesByRepoLanguageName
	if r.hotCache.nameHits != 0 {
		t.Fatalf("first page should not hit the name cache, hits=%d", r.hotCache.nameHits)
	}

	// Simulate the page boundary: page caches drop, the hot cache survives.
	r.nodesByRepoLanguageName = nil
	r.nodesByRepoName = nil
	r.nodesByExternLanguageName = nil
	r.nodeByID = map[string]*graph.Node{src.ID: src}

	if _, _, err := r.warmRepoLanguageNameCache(page); err != nil {
		t.Fatal(err)
	}
	if r.hotCache.nameHits == 0 {
		t.Fatal("second page should answer name groups from the hot cache")
	}

	// The rebuilt page cache must be equivalent: same candidate IDs for the
	// warmed name in the same scope.
	for scope, byName := range firstHits {
		secondByName, ok := r.nodesByRepoLanguageName[scope]
		if !ok {
			t.Fatalf("scope %v missing from second page cache", scope)
		}
		for name, nodes := range byName {
			secondNodes := secondByName[name]
			if len(nodes) != len(secondNodes) {
				t.Fatalf("name %q: first page %d candidates, second page %d", name, len(nodes), len(secondNodes))
			}
			for i := range nodes {
				if nodes[i].ID != secondNodes[i].ID {
					t.Fatalf("name %q candidate %d differs: %s vs %s", name, i, nodes[i].ID, secondNodes[i].ID)
				}
			}
		}
	}
}
