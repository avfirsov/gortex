// Package packeval is the held-out retrieval-precision harness for
// context packing. It scores the real retrieval stack against curated
// gold fixtures on Precision@K / Recall@K / MRR, and A/Bs the pluggable
// pack strategies (top-k / density / file-grouped) under a fixed token
// budget — the offline measurement Gortex previously lacked (it tuned
// rerank weights only from online feedback telemetry).
//
// The harness is provider-driven: a RankedProvider returns the ranked,
// token-costed candidate set for a query (wired to the live engine +
// rerank pipeline by the `gortex eval pack` CLI). For each strategy the
// harness packs the candidates into the budget, scores the delivered
// top-K against the fixture gold, and aggregates overall and per-tier.
package packeval

import (
	"fmt"
	"sort"
	"strings"

	"github.com/zzet/gortex/internal/eval/recall"
	"github.com/zzet/gortex/internal/search/packstrategy"
)

// RankedProvider returns the ranked candidate items for a query, best
// first, each carrying its file and token cost so a strategy can pack
// it. limit caps how many candidates to gather before packing.
type RankedProvider func(query string, limit int) []packstrategy.Item

// Metrics holds the standard retrieval-precision numbers for one slice
// of cases.
type Metrics struct {
	Cases       int     `json:"cases"`
	PrecisionAtK float64 `json:"precision_at_k"`
	RecallAtK    float64 `json:"recall_at_k"`
	MRR          float64 `json:"mrr"`
}

// StrategyResult aggregates a strategy's run over the whole fixture.
type StrategyResult struct {
	Strategy     string                 `json:"strategy"`
	Overall      Metrics                `json:"overall"`
	PerTier      map[recall.Tier]Metrics `json:"per_tier"`
	MeanSelected float64                `json:"mean_selected"`   // avg symbols packed
	MeanTokens   float64                `json:"mean_tokens"`     // avg tokens packed
}

// Report bundles the sweep.
type Report struct {
	Fixture     string           `json:"fixture"`
	K           int              `json:"k"`
	TokenBudget int              `json:"token_budget"`
	FetchLimit  int              `json:"fetch_limit"`
	Cases       int              `json:"cases"`
	Strategies  []StrategyResult `json:"strategies"`
}

// Options configure a sweep.
type Options struct {
	Strategies  []packstrategy.Strategy // default: packstrategy.All()
	K           int                     // precision/recall cutoff (default 10)
	TokenBudget int                     // pack budget (default 8000)
	FetchLimit  int                     // candidates gathered before packing (default 50)
}

func (o Options) withDefaults() Options {
	if len(o.Strategies) == 0 {
		o.Strategies = packstrategy.All()
	}
	if o.K <= 0 {
		o.K = 10
	}
	if o.TokenBudget <= 0 {
		o.TokenBudget = 8000
	}
	if o.FetchLimit <= 0 {
		o.FetchLimit = 50
	}
	return o
}

// Run sweeps every strategy over the fixture and returns the report.
// The provider is invoked once per case per — its result is reused
// across strategies (packing is pure), so retrieval cost is paid once.
func Run(fixture recall.Fixture, provider RankedProvider, opts Options) Report {
	opts = opts.withDefaults()
	rep := Report{
		Fixture:     fixture.Name,
		K:           opts.K,
		TokenBudget: opts.TokenBudget,
		FetchLimit:  opts.FetchLimit,
		Cases:       len(fixture.Cases),
	}

	// Gather candidates once per case (retrieval is the expensive part;
	// packing each strategy over the cached candidates is cheap).
	cands := make([][]packstrategy.Item, len(fixture.Cases))
	for i, c := range fixture.Cases {
		cands[i] = provider(c.Query, opts.FetchLimit)
	}

	for _, strat := range opts.Strategies {
		rep.Strategies = append(rep.Strategies, runStrategy(fixture, cands, strat, opts))
	}
	return rep
}

func runStrategy(fixture recall.Fixture, cands [][]packstrategy.Item, strat packstrategy.Strategy, opts Options) StrategyResult {
	type acc struct {
		cases             int
		sumP, sumR, sumMR float64
	}
	overall := &acc{}
	perTier := map[recall.Tier]*acc{}
	var selectedSum, tokensSum float64

	for i, c := range fixture.Cases {
		selected := packstrategy.Select(strat, cands[i], opts.TokenBudget)
		selectedSum += float64(len(selected))
		toks := 0
		for _, it := range selected {
			toks += it.Tokens
		}
		tokensSum += float64(toks)

		p, r, mrr := scoreCase(selected, c.Expected, opts.K)
		overall.cases++
		overall.sumP += p
		overall.sumR += r
		overall.sumMR += mrr

		tier := c.Tier
		if tier == "" {
			tier = recall.TierExact
		}
		a := perTier[tier]
		if a == nil {
			a = &acc{}
			perTier[tier] = a
		}
		a.cases++
		a.sumP += p
		a.sumR += r
		a.sumMR += mrr
	}

	res := StrategyResult{
		Strategy: string(packstrategy.Normalize(string(strat))),
		Overall:  finalize(overall.cases, overall.sumP, overall.sumR, overall.sumMR),
		PerTier:  make(map[recall.Tier]Metrics, len(perTier)),
	}
	for tier, a := range perTier {
		res.PerTier[tier] = finalize(a.cases, a.sumP, a.sumR, a.sumMR)
	}
	if n := float64(len(fixture.Cases)); n > 0 {
		res.MeanSelected = selectedSum / n
		res.MeanTokens = tokensSum / n
	}
	return res
}

func finalize(cases int, sumP, sumR, sumMR float64) Metrics {
	m := Metrics{Cases: cases}
	if cases > 0 {
		n := float64(cases)
		m.PrecisionAtK = sumP / n
		m.RecallAtK = sumR / n
		m.MRR = sumMR / n
	}
	return m
}

// scoreCase computes Precision@K, Recall@K, and reciprocal rank for one
// case against its gold expected set. A case is relevant-at-rank when a
// delivered item's ID appears in the gold set.
func scoreCase(selected []packstrategy.Item, expected []string, k int) (precision, recallV, mrr float64) {
	gold := make(map[string]struct{}, len(expected))
	for _, id := range expected {
		gold[id] = struct{}{}
	}
	if len(gold) == 0 {
		return 0, 0, 0
	}
	hits := 0
	firstHit := 0
	for i, it := range selected {
		if i >= k {
			break
		}
		if _, ok := gold[it.ID]; ok {
			hits++
			if firstHit == 0 {
				firstHit = i + 1
			}
		}
	}
	precision = float64(hits) / float64(k)
	recallV = float64(hits) / float64(len(gold))
	if recallV > 1 {
		recallV = 1
	}
	if firstHit > 0 {
		mrr = 1.0 / float64(firstHit)
	}
	return precision, recallV, mrr
}

// Markdown renders the sweep as a diffable report.
func Markdown(rep Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Pack-strategy retrieval eval\n\n")
	fmt.Fprintf(&b, "_Fixture: `%s` · %d cases · K=%d · token_budget=%d · fetch_limit=%d_\n\n",
		rep.Fixture, rep.Cases, rep.K, rep.TokenBudget, rep.FetchLimit)

	b.WriteString("## Overall\n\n")
	fmt.Fprintf(&b, "| strategy | P@%d | R@%d | MRR | mean symbols | mean tokens |\n", rep.K, rep.K)
	b.WriteString("|----------|------|------|-----|--------------|-------------|\n")
	strats := append([]StrategyResult(nil), rep.Strategies...)
	sort.SliceStable(strats, func(i, j int) bool {
		return strats[i].Overall.PrecisionAtK > strats[j].Overall.PrecisionAtK
	})
	for _, s := range strats {
		fmt.Fprintf(&b, "| %s | %5.1f%% | %5.1f%% | %.3f | %.1f | %.0f |\n",
			s.Strategy, s.Overall.PrecisionAtK*100, s.Overall.RecallAtK*100, s.Overall.MRR,
			s.MeanSelected, s.MeanTokens)
	}

	b.WriteString("\n## Per tier (P@K)\n\n")
	b.WriteString("| strategy | exact | concept | multi_hop |\n")
	b.WriteString("|----------|-------|---------|-----------|\n")
	for _, s := range strats {
		fmt.Fprintf(&b, "| %s | %5.1f%% | %5.1f%% | %5.1f%% |\n",
			s.Strategy,
			s.PerTier[recall.TierExact].PrecisionAtK*100,
			s.PerTier[recall.TierConcept].PrecisionAtK*100,
			s.PerTier[recall.TierMultiHop].PrecisionAtK*100,
		)
	}
	return b.String()
}
