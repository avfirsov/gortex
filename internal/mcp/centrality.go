package mcp

import (
	"github.com/zzet/gortex/internal/analysis"
)

// personalizedPageRank runs a Random-Walk-with-Restart (Personalized
// PageRank) from the given seed node IDs over the adjacency snapshot
// and returns each reachable node's proximity score. It is the seam the
// rerank pipeline's ProximitySignal (and context_closure's proximity
// mode) reach centrality through.
//
// Walks flow through a Merkle-keyed cache (see ppr_cache.go) so repeated
// walks on an unchanged graph — or on packages that did not change
// between snapshots — return instantly instead of re-iterating the whole
// CSR. The cache is bypassed when disabled (GORTEX_PPR_CACHE_DISABLE) or
// when the snapshot has no package roots.
func (s *Server) personalizedPageRank(snap *analysis.AdjacencySnapshot, seeds []string) map[string]float64 {
	if snap == nil || len(seeds) == 0 {
		return nil
	}
	cache := s.pprCache
	if cache == nil || !cache.enabled {
		return snap.PersonalizedPageRank(seeds, 0)
	}
	// Merkle-keyed walk cache: the key embeds the per-package content
	// roots the walk depends on, so an unchanged walk hits even across
	// a snapshot rebuild, and only a walk touching a changed package
	// recomputes. An empty key (no package roots, or no seed resolves)
	// falls through to an uncached walk.
	key := snap.WalkCacheKey(seeds, 0)
	if key != "" {
		if scores, ok := cache.get(key); ok {
			return scores
		}
	}
	scores := snap.PersonalizedPageRank(seeds, 0)
	cache.put(key, scores)
	return scores
}
