package quality

import (
	"math"
	"strings"
	"testing"
)

// Test rankers: stub ranker funcs that return fixed lists per query
// — keeps the replay tests deterministic and avoids pulling in the
// real engine.

func staticRanker(byQuery map[string][]string) RankerFunc {
	return func(q string, topK int) []string {
		out := byQuery[q]
		if topK > 0 && len(out) > topK {
			return out[:topK]
		}
		return out
	}
}

func TestReplay_IdenticalRankersZeroChurn(t *testing.T) {
	baseline := staticRanker(map[string][]string{"q1": {"a", "b", "c"}})
	candidate := baseline

	queries := []ReplayQuery{{Query: "q1"}}
	got, err := Replay(queries, baseline, candidate, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got.Top1ChurnPct != 0 {
		t.Errorf("identical rankers should have 0%% top1 churn, got %.2f%%", got.Top1ChurnPct)
	}
	if got.MeanKendall < 0.99 {
		t.Errorf("identical rankers should have kendall ≈ 1, got %.4f", got.MeanKendall)
	}
	if got.MeanTop5Churn != 0 {
		t.Errorf("identical rankers should have 0 top5 changes, got %.2f", got.MeanTop5Churn)
	}
}

func TestReplay_OppositeRankersHighChurn(t *testing.T) {
	baseline := staticRanker(map[string][]string{"q1": {"a", "b", "c", "d", "e"}})
	candidate := staticRanker(map[string][]string{"q1": {"e", "d", "c", "b", "a"}})

	queries := []ReplayQuery{{Query: "q1"}}
	got, err := Replay(queries, baseline, candidate, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got.Top1ChurnPct != 100 {
		t.Errorf("reversed ranker should have 100%% top1 churn, got %.2f%%", got.Top1ChurnPct)
	}
	// Kendall τ for reversed order over 5 elements: all pairs are
	// discordant → τ = -1.
	if got.MeanKendall > -0.99 {
		t.Errorf("reversed rankers should have kendall ≈ -1, got %.4f", got.MeanKendall)
	}
}

func TestReplay_RecallDelta(t *testing.T) {
	baseline := staticRanker(map[string][]string{"q1": {"miss1", "miss2", "a"}})
	candidate := staticRanker(map[string][]string{"q1": {"a", "miss1", "miss2"}})

	queries := []ReplayQuery{
		{Query: "q1", Expected: []string{"a"}},
	}
	got, err := Replay(queries, baseline, candidate, 10)
	if err != nil {
		t.Fatal(err)
	}
	// Both find "a" within top 10 → both recall = 1.0 → delta = 0.
	if math.Abs(got.RecallDelta) > 0.01 {
		t.Errorf("both-find recall delta = %.4f, want 0", got.RecallDelta)
	}
}

func TestReplay_RecallDelta_CandidateBetter(t *testing.T) {
	// Baseline finds nothing in top-K, candidate finds the expected.
	baseline := staticRanker(map[string][]string{"q1": {"x", "y", "z"}})
	candidate := staticRanker(map[string][]string{"q1": {"a"}})

	queries := []ReplayQuery{{Query: "q1", Expected: []string{"a"}}}
	got, _ := Replay(queries, baseline, candidate, 10)
	if got.RecallDelta <= 0 {
		t.Errorf("candidate-better should yield positive recall delta, got %.4f", got.RecallDelta)
	}
}

func TestReplay_NilRankerErrors(t *testing.T) {
	_, err := Replay(nil, nil, staticRanker(nil), 10)
	if err == nil {
		t.Error("nil baseline should error")
	}
}

func TestKendallTauTopK_PerfectMatch(t *testing.T) {
	tau := kendallTauTopK([]string{"a", "b", "c"}, []string{"a", "b", "c"}, 10)
	if tau != 1 {
		t.Errorf("identical lists τ = %.4f, want 1", tau)
	}
}

func TestKendallTauTopK_FullyReversed(t *testing.T) {
	tau := kendallTauTopK([]string{"a", "b", "c"}, []string{"c", "b", "a"}, 10)
	if tau != -1 {
		t.Errorf("reversed lists τ = %.4f, want -1", tau)
	}
}

func TestKendallTauTopK_PartialOverlap(t *testing.T) {
	// Only "b" and "c" overlap; in baseline they're in order (b,c);
	// in candidate they're reversed (c,b). Single discordant pair
	// out of 1 total → τ = -1.
	tau := kendallTauTopK(
		[]string{"a", "b", "c", "d"},
		[]string{"e", "c", "b", "f"},
		10,
	)
	if tau != -1 {
		t.Errorf("partial-overlap τ = %.4f, want -1", tau)
	}
}

func TestKendallTauTopK_TooFewSharedReturnsOne(t *testing.T) {
	// Less than 2 shared elements → τ undefined; we return 1 to
	// mean "no measurable disagreement".
	tau := kendallTauTopK([]string{"a", "b"}, []string{"c", "d"}, 10)
	if tau != 1 {
		t.Errorf("disjoint lists τ = %.4f, want 1 (no measurable disagreement)", tau)
	}
}

func TestSetSymDiffSize(t *testing.T) {
	if got := setSymDiffSize([]string{"a", "b"}, []string{"b", "c"}); got != 2 {
		t.Errorf("symdiff = %d, want 2 (a and c are unique)", got)
	}
	if got := setSymDiffSize([]string{"a"}, []string{"a"}); got != 0 {
		t.Errorf("symdiff identical = %d, want 0", got)
	}
}

func TestRecallAtK_BoundaryCases(t *testing.T) {
	// No expected → 0.
	if got := recallAtK([]string{"a"}, nil, 10); got != 0 {
		t.Errorf("empty expected = %.4f, want 0", got)
	}
	// All expected found.
	if got := recallAtK([]string{"a", "b"}, []string{"a", "b"}, 10); got != 1 {
		t.Errorf("full recall = %.4f, want 1", got)
	}
	// Half found.
	if got := recallAtK([]string{"a"}, []string{"a", "b"}, 10); got != 0.5 {
		t.Errorf("half recall = %.4f, want 0.5", got)
	}
	// Cut by k.
	if got := recallAtK([]string{"x", "y", "a"}, []string{"a"}, 2); got != 0 {
		t.Errorf("k-cut recall = %.4f, want 0 (a is outside top-2)", got)
	}
}

func TestTop1Differs(t *testing.T) {
	if !top1Differs([]string{"a"}, []string{"b"}) {
		t.Error("different top1 should report changed")
	}
	if top1Differs([]string{"a"}, []string{"a"}) {
		t.Error("same top1 should not report changed")
	}
	if !top1Differs([]string{}, []string{"a"}) {
		t.Error("empty vs non-empty top1 should report changed")
	}
	if top1Differs(nil, nil) {
		t.Error("both empty should not report changed")
	}
}

// Quiet "imported and not used" if a future helper needs it.
var _ = strings.HasPrefix
