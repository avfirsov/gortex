package rerank

import (
	"math"
	"reflect"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestContextLoadsAnalysisMetricsOncePerCandidateBatch(t *testing.T) {
	candidates := []*Candidate{
		{Node: &graph.Node{ID: "b", Name: "B"}},
		{Node: &graph.Node{ID: "a", Name: "A"}},
	}
	calls := 0
	ctx := &Context{
		AnalysisMetricsOf: func(ids []string) map[string]AnalysisMetric {
			calls++
			if !reflect.DeepEqual(ids, []string{"b", "a"}) {
				t.Fatalf("candidate order changed: %v", ids)
			}
			return map[string]AnalysisMetric{
				"a": {CommunityID: "community-1", Authority: 0.8, Hub: 0.2},
				"b": {CommunityID: "community-1", Authority: 0.4, Hub: 0.1},
			}
		},
		CommunityOf: func(string) string { t.Fatal("whole-map community fallback called"); return "" },
		AuthorityOf: func(string) float64 { t.Fatal("whole-map HITS fallback called"); return 0 },
		HubOf:       func(string) float64 { t.Fatal("whole-map HITS fallback called"); return 0 },
	}
	ctx.Prepare(candidates)
	if calls != 1 {
		t.Fatalf("analysis provider calls=%d want=1", calls)
	}
	if got := ctx.communityCount["community-1"]; got != 2 {
		t.Fatalf("community count=%d want=2", got)
	}
	got := (HITSSignal{}).Contribute("", candidates[1], ctx)
	want := 0.8 / 1.2
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("HITS contribution=%g want=%g", got, want)
	}
}

func TestContextExposesTruncatedCentralityWithoutSilentDamping(t *testing.T) {
	candidate := &Candidate{Node: &graph.Node{ID: "a", Name: "A"}}
	ctx := &Context{
		BatchedCentrality: func(seeds, candidates []string) CentralityResult {
			return CentralityResult{
				Scores: map[string]float64{"a": 0.75}, NodeCount: 7, EdgeCount: 9,
				NodeBatches: 2, EdgeBatches: 1, Truncated: true,
			}
		},
	}
	ctx.Prepare([]*Candidate{candidate})
	receipt := ctx.CentralityTelemetry()
	if !receipt.Truncated || receipt.NodeCount != 7 || receipt.EdgeCount != 9 || receipt.Scores != nil {
		t.Fatalf("unexpected centrality receipt: %+v", receipt)
	}
	if got := (ProximitySignal{}).Contribute("", candidate, ctx); got != 1 {
		t.Fatalf("bounded centrality was silently damped: got=%g want=1", got)
	}
}
