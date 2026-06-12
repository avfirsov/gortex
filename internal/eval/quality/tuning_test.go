package quality

import (
	"strings"
	"testing"
)

func TestSuggestWeights_UsefulPushesUp(t *testing.T) {
	in := []SignalFeedback{
		{Signal: "bm25", Weight: 1.0, UsefulHits: 80, MissedHits: 20},
	}
	out := SuggestWeights(in, 1.0)
	if len(out) != 1 {
		t.Fatalf("got %d rows, want 1", len(out))
	}
	if out[0].SuggestedWeight <= out[0].Weight {
		t.Errorf("useful > missed should push weight UP; got %.3f → %.3f", out[0].Weight, out[0].SuggestedWeight)
	}
}

func TestSuggestWeights_MissedPushesDown(t *testing.T) {
	in := []SignalFeedback{
		{Signal: "churn", Weight: 0.5, UsefulHits: 5, MissedHits: 50},
	}
	out := SuggestWeights(in, 1.0)
	if out[0].SuggestedWeight >= out[0].Weight {
		t.Errorf("missed > useful should push weight DOWN; got %.3f → %.3f", out[0].Weight, out[0].SuggestedWeight)
	}
}

func TestSuggestWeights_NoDataKeepsWeight(t *testing.T) {
	in := []SignalFeedback{
		{Signal: "minhash", Weight: 0.3, UsefulHits: 0, MissedHits: 0},
	}
	out := SuggestWeights(in, 1.0)
	if out[0].SuggestedWeight != out[0].Weight {
		t.Errorf("no-data should keep weight unchanged; got %.3f → %.3f", out[0].Weight, out[0].SuggestedWeight)
	}
	if !strings.Contains(out[0].Reasoning, "insufficient data") {
		t.Errorf("no-data reasoning should mention insufficient data; got %q", out[0].Reasoning)
	}
}

func TestSuggestWeights_TiePicksNoChange(t *testing.T) {
	in := []SignalFeedback{
		{Signal: "fan_in", Weight: 0.6, UsefulHits: 10, MissedHits: 10},
	}
	out := SuggestWeights(in, 1.0)
	if out[0].SuggestedWeight != out[0].Weight {
		t.Errorf("tie should keep weight; got %.3f → %.3f", out[0].Weight, out[0].SuggestedWeight)
	}
}

func TestSuggestWeights_NudgeCapped(t *testing.T) {
	// Even with absurdly high useful counts, the nudge per call is
	// capped at 0.1 so a single run can't wildly swing weights.
	in := []SignalFeedback{
		{Signal: "fan_in", Weight: 0.5, UsefulHits: 1_000_000, MissedHits: 0},
	}
	out := SuggestWeights(in, 1.0)
	delta := out[0].SuggestedWeight - out[0].Weight
	if delta > 0.101 {
		t.Errorf("nudge should be capped at 0.1, got %+.3f", delta)
	}
}

func TestSuggestWeights_DownNudgeCanNotGoNegative(t *testing.T) {
	in := []SignalFeedback{
		{Signal: "rare", Weight: 0.05, UsefulHits: 0, MissedHits: 1_000_000},
	}
	out := SuggestWeights(in, 1.0)
	if out[0].SuggestedWeight < 0 {
		t.Errorf("suggested weight should never go negative, got %.3f", out[0].SuggestedWeight)
	}
}

func TestSuggestWeights_SortedByAbsoluteDelta(t *testing.T) {
	in := []SignalFeedback{
		{Signal: "small_change", Weight: 1.0, UsefulHits: 5, MissedHits: 4},
		{Signal: "big_change", Weight: 1.0, UsefulHits: 90, MissedHits: 10},
		{Signal: "no_change", Weight: 1.0, UsefulHits: 0, MissedHits: 0},
	}
	out := SuggestWeights(in, 1.0)
	if out[0].Signal != "big_change" {
		t.Errorf("first row should be the biggest change; got %s", out[0].Signal)
	}
}

func TestRenderTuningMarkdown_HasHeaderAndRows(t *testing.T) {
	rows := []SignalFeedback{
		{Signal: "bm25", Weight: 1.0, UsefulHits: 80, MissedHits: 20, SuggestedWeight: 1.06, Reasoning: "useful > missed"},
	}
	md := RenderTuningMarkdown(rows)
	for _, want := range []string{
		"# Rerank weight-tuning suggestion",
		"| bm25 |",
		"1.000",
		"1.060",
		"useful > missed",
		"**Substantial nudges",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q\n----\n%s", want, md)
		}
	}
}

func TestRenderTuningMarkdown_SubstantialCount(t *testing.T) {
	rows := []SignalFeedback{
		{Signal: "tiny",    Weight: 1.0, SuggestedWeight: 1.01}, // not substantial
		{Signal: "big1",    Weight: 1.0, SuggestedWeight: 1.10}, // substantial
		{Signal: "big2",    Weight: 1.0, SuggestedWeight: 0.90}, // substantial (negative)
		{Signal: "nothing", Weight: 1.0, SuggestedWeight: 1.0},  // not substantial
	}
	md := RenderTuningMarkdown(rows)
	if !strings.Contains(md, "Substantial nudges (|Δ| ≥ 0.05): 2 / 4") {
		t.Errorf("substantial count wrong:\n%s", md)
	}
}
