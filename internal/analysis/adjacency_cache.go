package analysis

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

// Content-addressed cache keying for seeded random walks.
//
// A Personalized-PageRank result over a seed set depends on the
// reachable subgraph. Recomputing it on every query is wasteful when
// the graph — or the part of it the walk touches — has not changed.
// These helpers derive a per-package Merkle root for the snapshot and a
// content-addressed cache key for a walk, so the MCP server's walk cache
// (see internal/mcp/ppr_cache.go) can serve an unchanged walk instantly
// and only recompute when a package the walk depends on actually
// changed — the incremental-RWR property a whole-graph version number
// (NodeCount/EdgeCount) cannot provide.

const (
	fnvOffset64 uint64 = 14695981039346656037
	fnvPrime64  uint64 = 1099511628211
)

// fnvStr folds a string into a running FNV-1a hash.
func fnvStr(h uint64, s string) uint64 {
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= fnvPrime64
	}
	return h
}

// fnvU64 folds a uint64 into a running FNV-1a hash.
func fnvU64(h, v uint64) uint64 {
	for i := 0; i < 8; i++ {
		h ^= (v >> (8 * i)) & 0xff
		h *= fnvPrime64
	}
	return h
}

// packageOfID returns the package directory for a node ID — the
// directory of the file-path portion (everything before "::"). For
// "gortex/internal/mcp/server.go::NewServer" that is
// "gortex/internal/mcp". A path with no slash maps to "" (repo root).
// This is the granularity at which the walk cache is invalidated.
func packageOfID(id string) string {
	path := id
	if i := strings.Index(path, "::"); i >= 0 {
		path = path[:i]
	}
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[:i]
	}
	return ""
}

// computePackageRoots builds the per-package content roots from the CSR.
// For each node it folds the node's stable ID plus its out-edges
// (neighbour string IDs + weights) into the node's content hash, then
// sums those hashes per package. Summation is commutative, so the root
// is independent of node iteration order; it uses string IDs (not dense
// indices), so a package's root is invariant to index shifts caused by
// edits in OTHER packages — the property that makes the cache truly
// incremental.
func computePackageRoots(ids []string, offsets []int32, neighbors []int32, weights []float64) map[string]uint64 {
	roots := make(map[string]uint64, len(ids)/8+1)
	for i := 0; i < len(ids); i++ {
		h := fnvStr(fnvOffset64, ids[i])
		h ^= 0 // separator marker folded below
		h = fnvU64(h, 0x1f)
		start, end := offsets[i], offsets[i+1]
		for k := start; k < end; k++ {
			h = fnvStr(h, ids[neighbors[k]])
			h = fnvU64(h, math.Float64bits(weights[k]))
		}
		roots[packageOfID(ids[i])] += h
	}
	return roots
}

// WalkCacheKey derives a content-addressed cache key for a seeded walk.
// The key folds: the resolved (in-snapshot) seed IDs, the restart
// probability, and the per-package roots of the packages the walk
// depends on — the seed packages plus their 1-hop out-neighbour
// packages. Two walks with the same seeds and restart over graph states
// whose relevant packages are byte-for-byte identical produce the same
// key (cache hit); a change to any depended-on package changes that
// package's root and therefore the key (cache miss → recompute).
//
// Returns "" when the snapshot has no package roots (cache disabled) or
// no seed resolves to a snapshot node — the caller then computes
// uncached. The resolved-seed set matches exactly what
// PersonalizedPageRank walks, so the key and the result stay coherent.
func (a *AdjacencySnapshot) WalkCacheKey(seeds []string, restart float64) string {
	if a == nil || len(a.pkgRoots) == 0 || len(seeds) == 0 {
		return ""
	}
	if restart <= 0 || restart >= 1 {
		restart = pprDefaultRestart
	}

	resolved := make([]string, 0, len(seeds))
	seen := make(map[int]bool, len(seeds))
	pkgSet := make(map[string]struct{}, len(seeds)*4)
	for _, s := range seeds {
		i, ok := a.index[s]
		if !ok || seen[i] {
			continue
		}
		seen[i] = true
		resolved = append(resolved, s)
		pkgSet[packageOfID(s)] = struct{}{}
		// 1-hop forward neighbours: their packages also influence the
		// walk's first step, so fold them into the dependency set.
		for k := a.offsets[i]; k < a.offsets[i+1]; k++ {
			pkgSet[packageOfID(a.ids[a.neighbors[k]])] = struct{}{}
		}
	}
	if len(resolved) == 0 {
		return ""
	}
	sort.Strings(resolved)
	pkgs := make([]string, 0, len(pkgSet))
	for p := range pkgSet {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)

	h := fnvStr(fnvOffset64, "ppr\x00")
	for _, s := range resolved {
		h = fnvStr(h, s)
		h = fnvU64(h, 0x1f)
	}
	h = fnvU64(h, math.Float64bits(restart))
	for _, p := range pkgs {
		h = fnvStr(h, p)
		h = fnvU64(h, a.pkgRoots[p])
	}
	return strconv.FormatUint(h, 16)
}

// PackageRootCount returns the number of distinct packages with a
// content root. Exposed for diagnostics / tests.
func (a *AdjacencySnapshot) PackageRootCount() int {
	if a == nil {
		return 0
	}
	return len(a.pkgRoots)
}
