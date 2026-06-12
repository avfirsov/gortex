package rerank

import (
	"reflect"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func cand(id string, textRank, vectorRank int) *Candidate {
	return &Candidate{Node: &graph.Node{ID: id}, TextRank: textRank, VectorRank: vectorRank}
}

func TestSelectCentralitySeeds(t *testing.T) {
	cands := []*Candidate{
		cand("c", 2, -1),
		cand("a", 0, -1),
		cand("b", 1, -1),
		cand("v", -1, 0), // vector-only, best vector rank
		cand("z", -1, -1),
	}
	got := selectCentralitySeeds(cands, 3)
	want := []string{"a", "v", "b"} // ranks 0, 0, 1 — ties break on ID
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("seeds = %v, want %v", got, want)
	}
}

func TestProximitySignalNormalisesAndScores(t *testing.T) {
	cands := []*Candidate{
		cand("a", 0, -1),
		cand("b", 1, -1),
		cand("c", 2, -1),
	}
	called := false
	ctx := &Context{
		Centrality: func(seeds []string) map[string]float64 {
			called = true
			// seeds should be the top-ranked candidates.
			if len(seeds) == 0 || seeds[0] != "a" {
				t.Errorf("unexpected seeds %v", seeds)
			}
			// Raw, un-normalised scores; prepare must rescale to [0,1].
			return map[string]float64{"a": 10, "b": 5, "c": 0}
		},
	}
	ctx.prepare(cands)
	if !called {
		t.Fatal("Centrality closure was not invoked")
	}

	sig := ProximitySignal{}
	if got := sig.Contribute("", cands[0], ctx); got != 1.0 {
		t.Fatalf("proximity(a) = %v, want 1.0 (batch max)", got)
	}
	if got := sig.Contribute("", cands[1], ctx); got != 0.5 {
		t.Fatalf("proximity(b) = %v, want 0.5", got)
	}
	if got := sig.Contribute("", cands[2], ctx); got != 0 {
		t.Fatalf("proximity(c) = %v, want 0 (zero raw dropped)", got)
	}
}

func TestProximitySignalNilProviderDegrades(t *testing.T) {
	cands := []*Candidate{cand("a", 0, -1)}
	ctx := &Context{} // no Centrality wired
	ctx.prepare(cands)
	if got := (ProximitySignal{}).Contribute("", cands[0], ctx); got != 0 {
		t.Fatalf("proximity with no provider = %v, want 0", got)
	}
}

func TestProximityClassMultiplier(t *testing.T) {
	// Concept queries lean hardest on proximity; path/symbol dampen it.
	concept := ClassWeightMultiplier(QueryClassConcept, SignalProximity)
	path := ClassWeightMultiplier(QueryClassPath, SignalProximity)
	symbol := ClassWeightMultiplier(QueryClassSymbol, SignalProximity)
	if !(concept > symbol && symbol > path) {
		t.Fatalf("expected concept(%.2f) > symbol(%.2f) > path(%.2f)", concept, symbol, path)
	}
	if concept <= 1.0 {
		t.Fatalf("concept proximity multiplier should boost (>1.0), got %.2f", concept)
	}
	// Continuous lever: NL anchor reproduces the concept boost, the
	// most BM25-leaning anchor reproduces the path damping.
	if nl := continuousClassMultiplier(AlphaNL, SignalProximity); nl != concept {
		t.Fatalf("continuous NL proximity = %.3f, want concept %.3f", nl, concept)
	}
	if p := continuousClassMultiplier(AlphaPath, SignalProximity); p != path {
		t.Fatalf("continuous path proximity = %.3f, want path %.3f", p, path)
	}
}
