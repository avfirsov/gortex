package rerank

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestAlphaForContinuous_AnchorsAndBounds(t *testing.T) {
	cases := []struct {
		query string
		want  float64
	}{
		{"FooBar", AlphaSymbol},               // single identifier token
		{"validate_token", AlphaSymbol},       // snake_case identifier
		{"what does this do", AlphaNL},        // prose, stopword-heavy
		{"", AlphaNL},                         // empty
		{"internal/auth/token.go", AlphaPath}, // path shape
		{"func(ctx) error", AlphaSignature},   // signature shape
	}
	for _, c := range cases {
		if got := AlphaForContinuous(c.query); got != c.want {
			t.Errorf("AlphaForContinuous(%q) = %v, want %v", c.query, got, c.want)
		}
	}
	// Every result stays within the discrete envelope [AlphaPath, AlphaNL].
	for _, q := range []string{"validateToken handler", "auth middleware", "parse the jwt cache token please"} {
		got := AlphaForContinuous(q)
		if got < AlphaPath || got > AlphaNL {
			t.Errorf("AlphaForContinuous(%q) = %v, out of [%v,%v]", q, got, AlphaPath, AlphaNL)
		}
	}
}

func TestAlphaForContinuous_Monotonic(t *testing.T) {
	// More identifier-shaped → lower α (more BM25-leaning).
	twoIdents := AlphaForContinuous("validateToken parseJWT")
	mixed := AlphaForContinuous("validateToken handler")
	prose := AlphaForContinuous("validate the user token")
	if !(twoIdents <= mixed && mixed <= prose) {
		t.Errorf("expected α to rise with prose-ness: twoIdents=%v mixed=%v prose=%v", twoIdents, mixed, prose)
	}
	if twoIdents != AlphaSymbol {
		t.Errorf("two pure identifiers should pin to AlphaSymbol, got %v", twoIdents)
	}
}

func TestContinuousClassMultiplier_AnchorsAndMonotonic(t *testing.T) {
	// NL anchor reproduces the neutral 1.0/1.0 baseline exactly.
	if got := continuousClassMultiplier(AlphaNL, SignalBM25); got != 1.0 {
		t.Errorf("bm25 mult at AlphaNL = %v, want 1.0", got)
	}
	if got := continuousClassMultiplier(AlphaNL, SignalSemantic); got != 1.0 {
		t.Errorf("semantic mult at AlphaNL = %v, want 1.0", got)
	}
	// Path anchor reproduces the discrete path-class multipliers.
	if got := continuousClassMultiplier(AlphaPath, SignalBM25); !floatNear(got, classWeightTable[QueryClassPath].bm25, 1e-9) {
		t.Errorf("bm25 mult at AlphaPath = %v, want %v", got, classWeightTable[QueryClassPath].bm25)
	}
	if got := continuousClassMultiplier(AlphaPath, SignalSemantic); !floatNear(got, classWeightTable[QueryClassPath].semantic, 1e-9) {
		t.Errorf("semantic mult at AlphaPath = %v, want %v", got, classWeightTable[QueryClassPath].semantic)
	}
	// Non-text signals are never class-scaled.
	if got := continuousClassMultiplier(AlphaPath, SignalFanIn); got != 1.0 {
		t.Errorf("fan_in mult = %v, want 1.0 (only bm25/semantic are class-sensitive)", got)
	}
	// Monotonic: lowering α raises bm25 and lowers semantic.
	if !(continuousClassMultiplier(AlphaSymbol, SignalBM25) > continuousClassMultiplier(AlphaNL, SignalBM25)) {
		t.Errorf("lower α must raise bm25 multiplier")
	}
	if !(continuousClassMultiplier(AlphaSymbol, SignalSemantic) < continuousClassMultiplier(AlphaNL, SignalSemantic)) {
		t.Errorf("lower α must lower semantic multiplier")
	}
}

func TestRerank_ContinuousAlphaTunesTextVsSemantic(t *testing.T) {
	g := newTestGraph()
	textOnly := mustNode(g, "f.go::TextOnly", "TextOnly", graph.KindFunction)
	vecOnly := mustNode(g, "f.go::VecOnly", "VecOnly", graph.KindFunction)
	weights := map[string]float64{SignalBM25: 1.0, SignalSemantic: 1.0}
	p := New(DefaultSignals(), weights)

	score := func(alpha float64, id string) float64 {
		cands := []*Candidate{
			{Node: textOnly, TextRank: 0, VectorRank: -1},
			{Node: vecOnly, TextRank: -1, VectorRank: 0},
		}
		p.Rerank("q", cands, &Context{Graph: g, Alpha: alpha})
		for _, c := range cands {
			if c.Node.ID == id {
				return c.Score
			}
		}
		t.Fatalf("candidate %s missing from rerank output", id)
		return 0
	}

	// A BM25-leaning α (path end) lifts the text-only candidate and
	// trims the semantic-only one relative to the neutral NL α.
	if !(score(AlphaPath, textOnly.ID) > score(AlphaNL, textOnly.ID)) {
		t.Errorf("low α must raise the text-only candidate's score")
	}
	if !(score(AlphaPath, vecOnly.ID) < score(AlphaNL, vecOnly.ID)) {
		t.Errorf("low α must lower the semantic-only candidate's score")
	}
}
