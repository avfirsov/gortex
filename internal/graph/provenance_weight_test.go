package graph

import "testing"

func TestProvenanceWeight_Tiers(t *testing.T) {
	w := func(origin string) float64 {
		return ProvenanceWeight(&Edge{From: "a", To: "b", Kind: EdgeCalls, Origin: origin})
	}
	cases := []struct {
		origin string
		want   float64
	}{
		{OriginLSPResolved, provWeightLSP},
		{OriginLSPDispatch, provWeightLSP},
		{OriginASTResolved, ProvenanceWeightMax},
		{OriginASTInferred, provWeightASTInferred},
		{OriginTextMatched, ProvenanceWeightMin},
	}
	for _, c := range cases {
		if got := w(c.origin); got != c.want {
			t.Errorf("ProvenanceWeight(origin=%q) = %v, want %v", c.origin, got, c.want)
		}
	}
	// LSP and text tiers are attenuated below the ast_resolved baseline.
	if !(w(OriginLSPResolved) < ProvenanceWeightMax) {
		t.Errorf("lsp tier must weight below ast_resolved baseline")
	}
	if !(w(OriginTextMatched) < ProvenanceWeightMax) {
		t.Errorf("text tier must weight below ast_resolved baseline")
	}
}

func TestProvenanceWeight_BackfillsAndNilSafe(t *testing.T) {
	// A nil edge weights at the trusted baseline.
	if got := ProvenanceWeight(nil); got != ProvenanceWeightMax {
		t.Errorf("nil edge = %v, want %v", got, ProvenanceWeightMax)
	}
	// Unset Origin backfills via DefaultOriginFor: a structural import
	// edge is ast_resolved (baseline); a bare call edge with no
	// confidence falls to text_matched (the minimum).
	if got := ProvenanceWeight(&Edge{Kind: EdgeImports}); got != ProvenanceWeightMax {
		t.Errorf("structural import edge = %v, want %v", got, ProvenanceWeightMax)
	}
	if got := ProvenanceWeight(&Edge{Kind: EdgeCalls}); got != ProvenanceWeightMin {
		t.Errorf("bare unresolved call edge = %v, want %v", got, ProvenanceWeightMin)
	}
}
