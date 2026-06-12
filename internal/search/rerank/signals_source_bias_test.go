package rerank

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestSourceBiasSignal_BoostsSourceWhenTestCoOccurs(t *testing.T) {
	g := newTestGraph()
	src := addFileNode(g, "auth/token.go", "ValidateToken", graph.KindFunction)
	tst := addFileNode(g, "auth/token_test.go", "TestValidateToken", graph.KindFunction)
	cands := []*Candidate{candidateFor(src, 0, -1), candidateFor(tst, 1, -1)}
	ctx := &Context{Graph: g}
	ctx.prepare(cands)
	sig := SourceBiasSignal{}

	if got := sig.Contribute("validate token", cands[0], ctx); got != 1.0 {
		t.Errorf("source with co-occurring test got %v, want 1.0", got)
	}
	if got := sig.Contribute("validate token", cands[1], ctx); got != 0 {
		t.Errorf("test candidate got %v, want 0", got)
	}
}

func TestSourceBiasSignal_NoBoostWithoutTest(t *testing.T) {
	g := newTestGraph()
	src := addFileNode(g, "auth/token.go", "ValidateToken", graph.KindFunction)
	other := addFileNode(g, "auth/session.go", "NewSession", graph.KindFunction)
	cands := []*Candidate{candidateFor(src, 0, -1), candidateFor(other, 1, -1)}
	ctx := &Context{Graph: g}
	ctx.prepare(cands)
	sig := SourceBiasSignal{}

	if got := sig.Contribute("q", cands[0], ctx); got != 0 {
		t.Errorf("source without co-occurring test got %v, want 0 (batch-relative)", got)
	}
}

func TestTestNameStem(t *testing.T) {
	cases := map[string]string{
		"TestValidateToken":   "validatetoken",
		"validate_token_test": "validate_token",
		"Test_Validate":       "validate",
		"ValidateSpec":        "validate",
		"describeAuthSpec":    "describeauth",
	}
	for in, want := range cases {
		if got := testNameStem(in); got != want {
			t.Errorf("testNameStem(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSourceBiasSignal_NilSafety(t *testing.T) {
	sig := SourceBiasSignal{}
	if got := sig.Contribute("q", nil, &Context{}); got != 0 {
		t.Errorf("nil candidate got %v, want 0", got)
	}
	if got := sig.Contribute("q", &Candidate{Node: &graph.Node{Name: "X", FilePath: "x.go"}}, nil); got != 0 {
		t.Errorf("nil ctx got %v, want 0", got)
	}
}
