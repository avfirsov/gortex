package mcp

import (
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/search/packstrategy"
)

// applyPackStrategy re-orders a smart_context working set per the
// configured packing strategy (GORTEX_PACK_STRATEGY). The default,
// top-k, is rank-faithful and returns the input untouched — so existing
// callers are unaffected and pay nothing. density / file-grouped reorder
// by token density or file coherence; the subsequent count cap and
// graded-manifest budget then fill against the strategy's order. This is
// the exact strategy code the eval harness (gortex eval pack) A/Bs, so
// the winner it measures is the one production runs.
func (s *Server) applyPackStrategy(nodes []*graph.Node) []*graph.Node {
	strat := packstrategy.FromEnv()
	if strat == packstrategy.StrategyTopK || len(nodes) < 2 {
		return nodes
	}
	items := make([]packstrategy.Item, len(nodes))
	byID := make(map[string]*graph.Node, len(nodes))
	n := len(nodes)
	for i, nd := range nodes {
		if nd == nil {
			continue
		}
		// Preserve the incoming rank as a descending score so the
		// strategy's density math and file-sum ordering stay rank-aware.
		items[i] = packstrategy.Item{
			ID:       nd.ID,
			FilePath: nd.FilePath,
			Score:    float64(n - i),
			Tokens:   estimatePackTokensForNode(nd),
		}
		byID[nd.ID] = nd
	}
	// Budget 0 == pure reorder; the count cap / manifest budget do the
	// actual trimming downstream so this never drops a symbol on its own.
	selected := packstrategy.Select(strat, items, 0)
	out := make([]*graph.Node, 0, len(selected))
	for _, it := range selected {
		if nd := byID[it.ID]; nd != nil {
			out = append(out, nd)
		}
	}
	if len(out) == 0 {
		return nodes
	}
	return out
}

// estimatePackTokensForNode approximates a symbol's packed token cost
// from its line span (the pack carries roughly the body), with a floor
// so a one-line symbol still has a non-trivial cost. Cheap — no source
// read; used only to weight the density strategy.
func estimatePackTokensForNode(n *graph.Node) int {
	if n == nil {
		return 12
	}
	lines := n.EndLine - n.StartLine + 1
	if lines < 1 {
		lines = 1
	}
	const tokensPerLine = 11
	return lines*tokensPerLine + 8
}
