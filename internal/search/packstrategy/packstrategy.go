// Package packstrategy implements the pluggable context-packing
// strategies the retrieval eval harness A/Bs and smart_context selects
// with. Given a ranked candidate set and a token budget, a strategy
// decides which symbols make the pack and in what order — the choice
// that, under a fixed budget, determines what the agent actually sees.
//
// Strategies:
//   - top-k:        rank order, greedily add whatever fits (rank-faithful).
//   - density:      re-order by score-per-token, then add what fits — packs
//     the most signal into the budget (token efficiency).
//   - file-grouped: cluster by file, densest files first, keeping a file's
//     symbols together — favours coherent multi-symbol context over
//     scattered high-scoring singletons.
package packstrategy

import (
	"os"
	"sort"
	"strings"
)

// Strategy names a packing algorithm.
type Strategy string

const (
	StrategyTopK        Strategy = "top-k"
	StrategyDensity     Strategy = "density"
	StrategyFileGrouped Strategy = "file-grouped"
)

// DefaultStrategy is the packing strategy used when none is configured.
// Top-k (rank-faithful) is the default: the eval harness (internal/eval/
// packeval) measures it as the strongest on P@10 / R@10 / MRR — packing
// the highest-reranked symbols first beats re-ordering by token density,
// because the rerank score already encodes relevance the budget should
// honour. It also matches the historical smart_context ordering, so the
// default is a no-op for existing callers. Override with
// GORTEX_PACK_STRATEGY to experiment (the eval can re-derive the winner).
const DefaultStrategy = StrategyTopK

// All returns every strategy, for sweeps.
func All() []Strategy {
	return []Strategy{StrategyTopK, StrategyDensity, StrategyFileGrouped}
}

// Item is one ranked candidate to pack: its symbol ID, file, rank score,
// and token cost. Items are passed to Select in rank order (best first).
type Item struct {
	ID       string
	FilePath string
	Score    float64
	Tokens   int
}

// Normalize maps a free-form / aliased strategy name to a known Strategy,
// falling back to DefaultStrategy. Recognises a few aliases for ergonomics.
func Normalize(s string) Strategy {
	switch strings.ToLower(strings.TrimSpace(string(s))) {
	case "top-k", "topk", "top_k", "rank":
		return StrategyTopK
	case "density", "dense":
		return StrategyDensity
	case "file-grouped", "file_grouped", "filegrouped", "file":
		return StrategyFileGrouped
	default:
		return DefaultStrategy
	}
}

// FromEnv resolves the strategy from GORTEX_PACK_STRATEGY (then
// BENCH_PACK_STRATEGY for parity with sweep tooling), falling back to
// DefaultStrategy when neither is set.
func FromEnv() Strategy {
	if v := strings.TrimSpace(os.Getenv("GORTEX_PACK_STRATEGY")); v != "" {
		return Normalize(v)
	}
	if v := strings.TrimSpace(os.Getenv("BENCH_PACK_STRATEGY")); v != "" {
		return Normalize(v)
	}
	return DefaultStrategy
}

// Select packs items into tokenBudget per the strategy and returns the
// chosen items in delivery order. A non-positive budget means "no
// limit" — every item is returned in the strategy's order. Items with a
// non-positive token cost are treated as free (they always fit).
func Select(strategy Strategy, items []Item, tokenBudget int) []Item {
	switch Normalize(string(strategy)) {
	case StrategyDensity:
		return selectDensity(items, tokenBudget)
	case StrategyFileGrouped:
		return selectFileGrouped(items, tokenBudget)
	default:
		return selectTopK(items, tokenBudget)
	}
}

// fits reports whether adding tok to used stays within budget (budget<=0
// means unlimited).
func fits(used, tok, budget int) bool {
	if budget <= 0 {
		return true
	}
	if tok <= 0 {
		return true
	}
	return used+tok <= budget
}

// selectTopK keeps the given rank order and greedily adds each item that
// still fits the budget (skipping an over-large item but continuing, so
// the budget is filled rather than abandoned at the first overflow).
func selectTopK(items []Item, budget int) []Item {
	out := make([]Item, 0, len(items))
	used := 0
	for _, it := range items {
		if fits(used, it.Tokens, budget) {
			out = append(out, it)
			used += maxInt(it.Tokens, 0)
		}
	}
	return out
}

// selectDensity re-orders candidates by score-per-token (signal density)
// and then greedily fills the budget — the most informative tokens first.
func selectDensity(items []Item, budget int) []Item {
	idx := make([]int, len(items))
	for i := range items {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		da, db := density(items[idx[a]]), density(items[idx[b]])
		if da != db {
			return da > db
		}
		// Stable tie-break on original rank.
		return idx[a] < idx[b]
	})
	out := make([]Item, 0, len(items))
	used := 0
	for _, i := range idx {
		it := items[i]
		if fits(used, it.Tokens, budget) {
			out = append(out, it)
			used += maxInt(it.Tokens, 0)
		}
	}
	return out
}

// density is an item's score per token, with a floor on tokens so a
// zero-cost item doesn't divide by zero (it sorts by raw score).
func density(it Item) float64 {
	t := it.Tokens
	if t <= 0 {
		t = 1
	}
	return it.Score / float64(t)
}

// selectFileGrouped clusters candidates by file, orders files by their
// summed score (densest files first), keeps each file's symbols together
// in rank order, and greedily fills the budget file by file. Coherent
// multi-symbol context beats scattered singletons for comprehension.
func selectFileGrouped(items []Item, budget int) []Item {
	type group struct {
		file  string
		sum   float64
		items []Item
	}
	order := make([]string, 0)
	byFile := make(map[string]*group)
	for _, it := range items {
		g := byFile[it.FilePath]
		if g == nil {
			g = &group{file: it.FilePath}
			byFile[it.FilePath] = g
			order = append(order, it.FilePath)
		}
		g.items = append(g.items, it)
		g.sum += it.Score
	}
	groups := make([]*group, 0, len(order))
	for _, f := range order {
		groups = append(groups, byFile[f])
	}
	// Densest files first; SliceStable keeps first-seen order on ties.
	sort.SliceStable(groups, func(a, b int) bool {
		return groups[a].sum > groups[b].sum
	})

	out := make([]Item, 0, len(items))
	used := 0
	for _, g := range groups {
		for _, it := range g.items {
			if fits(used, it.Tokens, budget) {
				out = append(out, it)
				used += maxInt(it.Tokens, 0)
			}
		}
	}
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
