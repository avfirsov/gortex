package analysis

import (
	"fmt"
	"sort"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// buildClusteredGraph builds a graph with several well-separated
// dense clusters, one per directory ("package"). Each cluster is a
// ring of `perCluster` functions so it forms a cohesive community;
// clusters are linked by a single weak cross-package edge so the
// graph is connected but the community boundaries are clear.
//
// grow names the package directories that each get one extra
// function wired into their ring — the knob the tests use to make
// a controlled set of packages "change" between runs. A package not
// in `grow` is rebuilt byte-identically.
func buildClusteredGraph(t *testing.T, clusters, perCluster int, grow ...string) *graph.Graph {
	t.Helper()
	g := graph.New()

	growSet := make(map[string]bool, len(grow))
	for _, p := range grow {
		growSet[p] = true
	}

	pkgDir := func(c int) string { return fmt.Sprintf("pkg/mod%d", c) }
	nodeID := func(c, i int) string { return fmt.Sprintf("%s/f%d.go::Fn%d_%d", pkgDir(c), i, c, i) }

	addFn := func(c, i int) {
		g.AddNode(&graph.Node{
			ID:       nodeID(c, i),
			Kind:     graph.KindFunction,
			Name:     fmt.Sprintf("Fn%d_%d", c, i),
			FilePath: fmt.Sprintf("%s/f%d.go", pkgDir(c), i),
			Language: "go",
		})
	}
	call := func(from, to string) {
		g.AddEdge(&graph.Edge{From: from, To: to, Kind: graph.EdgeCalls})
	}

	for c := 0; c < clusters; c++ {
		n := perCluster
		if growSet[pkgDir(c)] {
			n++
		}
		for i := 0; i < n; i++ {
			addFn(c, i)
		}
		// Dense ring inside the cluster: every node calls the next
		// and the one after, so the induced subgraph is cohesive.
		for i := 0; i < n; i++ {
			call(nodeID(c, i), nodeID(c, (i+1)%n))
			call(nodeID(c, i), nodeID(c, (i+2)%n))
		}
	}
	// One weak inter-cluster edge per adjacent pair keeps the graph
	// connected without merging the communities.
	for c := 0; c+1 < clusters; c++ {
		call(nodeID(c, 0), nodeID(c+1, 0))
	}

	return g
}

// communitySignature maps every node to the *set* of nodes it
// shares a community with — a representation that is invariant to
// how communities are numbered. Two partitions with the same
// signature group the same nodes together even if the "community-N"
// ids differ.
func communitySignature(cr *CommunityResult) map[string]string {
	byComm := make(map[string][]string)
	for nid, cid := range cr.NodeToComm {
		byComm[cid] = append(byComm[cid], nid)
	}
	sig := make(map[string]string, len(cr.NodeToComm))
	for _, members := range byComm {
		sort.Strings(members)
		joined := fmt.Sprint(members)
		for _, m := range members {
			sig[m] = joined
		}
	}
	return sig
}

func TestDetectCommunitiesLeidenIncremental(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			// (a) The first call has no cache: it does a full
			// recompute and returns a populated cache to carry
			// forward.
			name: "first run computes and caches",
			run: func(t *testing.T) {
				g := buildClusteredGraph(t, 5, 6)
				result, cache, stats := DetectCommunitiesLeidenIncremental(g, nil)
				if result == nil || len(result.Communities) == 0 {
					t.Fatal("first run produced no communities")
				}
				if stats.Incremental {
					t.Error("first run should be a full recompute, not incremental")
				}
				if stats.FullRecomputeReason == "" {
					t.Error("first run should carry a full-recompute reason")
				}
				if cache == nil || cache.part == nil || len(cache.nodeComm) == 0 {
					t.Fatal("first run did not populate the cache")
				}
				if len(cache.pkgFingerprint) == 0 {
					t.Error("first run cached no package fingerprints")
				}
			},
		},
		{
			// (b) A re-run on the identical graph reuses the cache
			// and re-partitions nothing.
			name: "no change reuses cache and repartitions nothing",
			run: func(t *testing.T) {
				g := buildClusteredGraph(t, 5, 6)
				_, cache, _ := DetectCommunitiesLeidenIncremental(g, nil)

				// Same graph (rebuilt identically) → fingerprints
				// match → incremental with an empty changed set.
				g2 := buildClusteredGraph(t, 5, 6)
				result, _, stats := DetectCommunitiesLeidenIncremental(g2, cache)
				if !stats.Incremental {
					t.Fatalf("no-change re-run fell back to full recompute: %s", stats.FullRecomputeReason)
				}
				if stats.ChangedPackages != 0 {
					t.Errorf("no-change re-run saw %d changed packages, want 0", stats.ChangedPackages)
				}
				if stats.RepartitionedNodes != 0 {
					t.Errorf("no-change re-run re-partitioned %d nodes, want 0", stats.RepartitionedNodes)
				}
				if result == nil || len(result.Communities) == 0 {
					t.Fatal("no-change re-run produced no communities")
				}
			},
		},
		{
			// (c) After exactly one package changed, only that
			// package is re-partitioned and the unchanged packages
			// keep their grouping.
			name: "one changed package repartitions only that package",
			run: func(t *testing.T) {
				g := buildClusteredGraph(t, 5, 6)
				baseResult, cache, _ := DetectCommunitiesLeidenIncremental(g, nil)
				baseSig := communitySignature(baseResult)

				// mod2 grows by one function; every other package
				// is byte-identical.
				g2 := buildClusteredGraph(t, 5, 6, "pkg/mod2")
				result, _, stats := DetectCommunitiesLeidenIncremental(g2, cache)

				if !stats.Incremental {
					t.Fatalf("single-package change fell back to full recompute: %s", stats.FullRecomputeReason)
				}
				if stats.ChangedPackages != 1 {
					t.Errorf("expected exactly 1 changed package, got %d", stats.ChangedPackages)
				}
				if stats.RepartitionedNodes == 0 {
					t.Error("a changed package should re-partition some nodes")
				}
				// The repartitioned set is the changed package plus
				// its boundary — it must not be the whole graph.
				totalNodes := len(result.NodeToComm)
				if stats.RepartitionedNodes >= totalNodes {
					t.Errorf("re-partitioned %d of %d nodes — incremental path touched the whole graph",
						stats.RepartitionedNodes, totalNodes)
				}

				// Unchanged packages keep their community grouping:
				// any two nodes grouped together before are still
				// grouped together, and vice versa.
				newSig := communitySignature(result)
				for nid, want := range baseSig {
					// Skip nodes living in the changed package.
					if packageKey(nodePath(g2, nid)) == "pkg/mod2" {
						continue
					}
					got, ok := newSig[nid]
					if !ok {
						t.Errorf("node %s lost its community after incremental run", nid)
						continue
					}
					if !sameUnchangedGrouping(want, got, "pkg/mod2", g2) {
						t.Errorf("unchanged node %s changed community grouping:\n before: %s\n after:  %s",
							nid, want, got)
					}
				}
			},
		},
		{
			// (d) When most packages change the incremental path
			// bails out to a full recompute.
			name: "large change triggers full recompute fallback",
			run: func(t *testing.T) {
				g := buildClusteredGraph(t, 6, 6)
				_, cache, _ := DetectCommunitiesLeidenIncremental(g, nil)

				// Four of the six packages grow — the changed
				// fraction (0.66) exceeds the fallback ratio, so the
				// incremental path recomputes the whole graph.
				g2 := buildClusteredGraph(t, 6, 6,
					"pkg/mod0", "pkg/mod1", "pkg/mod2", "pkg/mod3")
				result, _, stats := DetectCommunitiesLeidenIncremental(g2, cache)
				if stats.Incremental {
					t.Errorf("a large change (%d/%d packages) should fall back to a full recompute",
						stats.ChangedPackages, stats.TotalPackages)
				}
				if stats.FullRecomputeReason == "" {
					t.Error("full recompute should name a reason")
				}
				if result == nil || len(result.Communities) == 0 {
					t.Fatal("full-recompute fallback produced no communities")
				}
			},
		},
		{
			// (e) The incremental result agrees with a from-scratch
			// full recompute on the regions that did not change.
			name: "incremental consistent with full recompute on unchanged regions",
			run: func(t *testing.T) {
				g := buildClusteredGraph(t, 5, 6)
				_, cache, _ := DetectCommunitiesLeidenIncremental(g, nil)

				g2 := buildClusteredGraph(t, 5, 6, "pkg/mod3")
				incrResult, _, stats := DetectCommunitiesLeidenIncremental(g2, cache)
				if !stats.Incremental {
					t.Fatalf("expected incremental path, got full recompute: %s", stats.FullRecomputeReason)
				}
				fullResult := DetectCommunitiesLeiden(g2)

				incrSig := communitySignature(incrResult)
				fullSig := communitySignature(fullResult)

				// Within each unchanged package the two partitions
				// must agree on which members cluster together. We
				// compare grouping restricted to a single package
				// (the cross-package wiring is identical, the dense
				// rings dominate, so each package is one community
				// in both).
				for c := 0; c < 5; c++ {
					pkg := fmt.Sprintf("pkg/mod%d", c)
					if pkg == "pkg/mod3" {
						continue // the changed package
					}
					if !agreeWithinPackage(incrSig, fullSig, pkg, g2) {
						t.Errorf("unchanged package %s clusters differently under incremental vs full recompute", pkg)
					}
				}
			},
		},
		{
			// (f) The incremental path is deterministic: the same
			// cache and graph yield the same partition every time.
			name: "deterministic across repeated runs",
			run: func(t *testing.T) {
				g := buildClusteredGraph(t, 5, 6)
				_, cache, _ := DetectCommunitiesLeidenIncremental(g, nil)

				// Re-run the same one-package change five times from
				// the same cache; the partition must be identical.
				var first map[string]string
				for run := 0; run < 5; run++ {
					gN := buildClusteredGraph(t, 5, 6, "pkg/mod1")
					result, _, stats := DetectCommunitiesLeidenIncremental(gN, cache)
					if !stats.Incremental {
						t.Fatalf("run %d unexpectedly fell back to full recompute", run)
					}
					sig := communitySignature(result)
					if first == nil {
						first = sig
						continue
					}
					if len(sig) != len(first) {
						t.Fatalf("run %d produced a different node count: %d vs %d", run, len(sig), len(first))
					}
					for nid, want := range first {
						if sig[nid] != want {
							t.Errorf("run %d non-deterministic for node %s:\n first: %s\n now:   %s",
								run, nid, want, sig[nid])
						}
					}
				}
			},
		},
		{
			// A nil graph-with-no-edges case: an empty graph must
			// not panic and must report a full recompute.
			name: "empty graph yields empty result without panic",
			run: func(t *testing.T) {
				g := graph.New()
				result, cache, stats := DetectCommunitiesLeidenIncremental(g, nil)
				if result == nil {
					t.Fatal("nil result on empty graph")
				}
				if len(result.Communities) != 0 {
					t.Errorf("empty graph produced %d communities", len(result.Communities))
				}
				if stats.Incremental {
					t.Error("empty graph cannot be incremental")
				}
				if cache == nil {
					t.Fatal("empty graph returned a nil cache")
				}
			},
		},
		{
			// A stale cache from a structurally different graph
			// must never yield a wrong partition — it falls back.
			name: "stale cache from a different graph falls back",
			run: func(t *testing.T) {
				gA := buildClusteredGraph(t, 4, 5)
				_, cacheA, _ := DetectCommunitiesLeidenIncremental(gA, nil)

				// A graph with the same package count but renamed
				// packages — no fingerprint overlap.
				gB := graph.New()
				for c := 0; c < 4; c++ {
					dir := fmt.Sprintf("svc/other%d", c)
					ids := make([]string, 5)
					for i := 0; i < 5; i++ {
						id := fmt.Sprintf("%s/g%d.go::G%d_%d", dir, i, c, i)
						ids[i] = id
						gB.AddNode(&graph.Node{ID: id, Kind: graph.KindFunction, Name: id, FilePath: fmt.Sprintf("%s/g%d.go", dir, i), Language: "go"})
					}
					for i := 0; i < 5; i++ {
						gB.AddEdge(&graph.Edge{From: ids[i], To: ids[(i+1)%5], Kind: graph.EdgeCalls})
						gB.AddEdge(&graph.Edge{From: ids[i], To: ids[(i+2)%5], Kind: graph.EdgeCalls})
					}
				}
				result, _, stats := DetectCommunitiesLeidenIncremental(gB, cacheA)
				if stats.Incremental {
					t.Error("a cache from a disjoint graph must not drive an incremental run")
				}
				if result == nil || len(result.Communities) == 0 {
					t.Fatal("fallback on a disjoint graph produced no communities")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

// nodePath returns a node's file path from the graph, "" if absent.
func nodePath(g *graph.Graph, id string) string {
	if n := g.GetNode(id); n != nil {
		return n.FilePath
	}
	return ""
}

// sameUnchangedGrouping reports whether two community-membership
// strings describe the same grouping once members living in the
// changed package are dropped from both. The incremental path can
// legitimately pull a changed-package node into an unchanged node's
// community (that is the point), so the unchanged node's *membership
// set restricted to unchanged nodes* is what must stay stable.
func sameUnchangedGrouping(before, after, changedPkg string, g *graph.Graph) bool {
	return restrictToUnchanged(before, changedPkg, g) == restrictToUnchanged(after, changedPkg, g)
}

// restrictToUnchanged parses a "[id id id]" membership string and
// re-renders it keeping only nodes outside the changed package.
func restrictToUnchanged(membership, changedPkg string, g *graph.Graph) string {
	trimmed := membership
	if len(trimmed) >= 2 && trimmed[0] == '[' && trimmed[len(trimmed)-1] == ']' {
		trimmed = trimmed[1 : len(trimmed)-1]
	}
	var kept []string
	start := 0
	for i := 0; i <= len(trimmed); i++ {
		if i == len(trimmed) || trimmed[i] == ' ' {
			if i > start {
				tok := trimmed[start:i]
				if packageKey(nodePath(g, tok)) != changedPkg {
					kept = append(kept, tok)
				}
			}
			start = i + 1
		}
	}
	sort.Strings(kept)
	return fmt.Sprint(kept)
}

// agreeWithinPackage reports whether two signatures cluster the
// members of one package the same way. Each package's dense ring
// makes it a single community in any reasonable partition, so we
// just check that all of the package's nodes share one signature
// within each map and that the within-package grouping matches.
func agreeWithinPackage(sigA, sigB map[string]string, pkg string, g *graph.Graph) bool {
	var ids []string
	for nid := range sigA {
		if packageKey(nodePath(g, nid)) == pkg {
			ids = append(ids, nid)
		}
	}
	if len(ids) < 2 {
		return true
	}
	// Build, for each map, the partition of `ids` into groups that
	// share a signature; compare the two partitions.
	groupsOf := func(sig map[string]string) map[string]string {
		// Map each id to a canonical group key = sorted list of
		// package peers it shares a community with.
		out := make(map[string]string, len(ids))
		for _, a := range ids {
			var peers []string
			for _, b := range ids {
				if sig[a] == sig[b] {
					peers = append(peers, b)
				}
			}
			sort.Strings(peers)
			out[a] = fmt.Sprint(peers)
		}
		return out
	}
	ga, gb := groupsOf(sigA), groupsOf(sigB)
	for _, id := range ids {
		if ga[id] != gb[id] {
			return false
		}
	}
	return true
}
