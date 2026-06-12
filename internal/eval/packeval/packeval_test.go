package packeval

import (
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/eval/recall"
	"github.com/zzet/gortex/internal/search/packstrategy"
)

// syntheticProvider returns a fixed ranked candidate set per query so
// the harness logic (packing + scoring + sweep) is tested without
// indexing a real repo.
func syntheticProvider(items map[string][]packstrategy.Item) RankedProvider {
	return func(query string, limit int) []packstrategy.Item {
		got := items[query]
		if limit > 0 && len(got) > limit {
			got = got[:limit]
		}
		return got
	}
}

func TestScoreCase(t *testing.T) {
	selected := []packstrategy.Item{
		{ID: "a"}, {ID: "gold1"}, {ID: "b"}, {ID: "gold2"},
	}
	p, r, mrr := scoreCase(selected, []string{"gold1", "gold2"}, 10)
	if p != 2.0/10.0 {
		t.Fatalf("P@10 = %v, want 0.2", p)
	}
	if r != 1.0 {
		t.Fatalf("R@10 = %v, want 1.0 (both gold found)", r)
	}
	if mrr != 1.0/2.0 {
		t.Fatalf("MRR = %v, want 0.5 (first gold at rank 2)", mrr)
	}
}

func TestScoreCaseCutoff(t *testing.T) {
	selected := []packstrategy.Item{{ID: "x"}, {ID: "y"}, {ID: "gold"}}
	// gold is at rank 3 but K=2 — outside the cutoff.
	p, r, mrr := scoreCase(selected, []string{"gold"}, 2)
	if p != 0 || r != 0 || mrr != 0 {
		t.Fatalf("gold outside K must score 0/0/0, got %v %v %v", p, r, mrr)
	}
}

func TestRunSweepDistinguishesStrategies(t *testing.T) {
	// One concept case: the gold symbol is token-cheap but ranked below
	// a fat irrelevant one. Under a tight budget, density packs the
	// lean gold; top-k spends the budget on the fat rank-1 miss.
	q := "find the thing"
	cands := map[string][]packstrategy.Item{
		q: {
			{ID: "fat-miss", FilePath: "a.go", Score: 10, Tokens: 800},
			{ID: "lean-gold", FilePath: "b.go", Score: 6, Tokens: 50},
			{ID: "lean-miss", FilePath: "b.go", Score: 5, Tokens: 50},
		},
	}
	fixture := recall.Fixture{
		Name: "synthetic",
		Cases: []recall.Case{
			{ID: "c1", Tier: recall.TierConcept, Query: q, Expected: []string{"lean-gold"}},
		},
	}
	rep := Run(fixture, syntheticProvider(cands), Options{
		Strategies:  []packstrategy.Strategy{packstrategy.StrategyTopK, packstrategy.StrategyDensity},
		K:           10,
		TokenBudget: 800, // fits the fat one alone, or both lean ones
		FetchLimit:  50,
	})

	byName := map[string]StrategyResult{}
	for _, s := range rep.Strategies {
		byName[s.Strategy] = s
	}
	topk := byName[string(packstrategy.StrategyTopK)]
	density := byName[string(packstrategy.StrategyDensity)]

	// top-k spends 800 on fat-miss (rank 1), then can't fit the lean ones
	// -> gold missed -> P@10 = 0.
	if topk.Overall.PrecisionAtK != 0 {
		t.Fatalf("top-k should miss the gold under a tight budget, P@10=%v", topk.Overall.PrecisionAtK)
	}
	// density orders lean-gold (0.12) and lean-miss (0.10) above fat (0.0125),
	// packs both lean -> gold hit -> P@10 > 0.
	if density.Overall.PrecisionAtK <= 0 {
		t.Fatalf("density should pack the lean gold, P@10=%v", density.Overall.PrecisionAtK)
	}
}

func TestMarkdownRenders(t *testing.T) {
	rep := Report{
		Fixture: "x", K: 10, TokenBudget: 8000, Cases: 1,
		Strategies: []StrategyResult{
			{Strategy: "density", Overall: Metrics{PrecisionAtK: 0.5, RecallAtK: 0.8, MRR: 0.6}, PerTier: map[recall.Tier]Metrics{}},
		},
	}
	md := Markdown(rep)
	if !strings.Contains(md, "Pack-strategy retrieval eval") || !strings.Contains(md, "density") {
		t.Fatalf("markdown missing content:\n%s", md)
	}
}

func TestFormatComprehensionStubAsker(t *testing.T) {
	entries := []ContextEntry{{ID: "f.go::Foo", Name: "Foo", Signature: "func Foo() error"}}
	questions := []ComprehensionQuestion{
		{Question: "What does Foo return?", Accept: []string{"error"}},
	}
	renderers := map[string]FormatRenderer{
		"plain": func(es []ContextEntry) string {
			var b strings.Builder
			for _, e := range es {
				b.WriteString(e.Name + " " + e.Signature + "\n")
			}
			return b.String()
		},
	}
	// Stub asker that "reads" the prompt and answers from it.
	ask := func(prompt string) (string, error) {
		if strings.Contains(prompt, "func Foo() error") {
			return "It returns an error.", nil
		}
		return "unknown", nil
	}
	rep := RunFormatComprehension(entries, questions, renderers, func(s string) int { return len(s) / 4 }, ask)
	if len(rep.Formats) != 1 || rep.Formats[0].Correct != 1 {
		t.Fatalf("expected 1 correct, got %+v", rep.Formats)
	}
}

func TestFormatComprehensionNilAskerSkips(t *testing.T) {
	renderers := map[string]FormatRenderer{"plain": func(es []ContextEntry) string { return "" }}
	rep := RunFormatComprehension(nil, nil, renderers, nil, nil)
	if len(rep.Formats) != 1 || rep.Formats[0].Skipped == "" {
		t.Fatalf("nil asker should skip, got %+v", rep.Formats)
	}
}
