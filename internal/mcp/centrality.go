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
// This is the uncached path; a Merkle-keyed walk cache hangs behind
// this same seam (see ppr_cache.go) so repeated walks on an unchanged
// graph — or on packages that did not change between snapshots — return
// instantly instead of re-iterating the whole CSR.
func (s *Server) personalizedPageRank(snap *analysis.AdjacencySnapshot, seeds []string) map[string]float64 {
	if snap == nil || len(seeds) == 0 {
		return nil
	}
	return snap.PersonalizedPageRank(seeds, 0)
}
