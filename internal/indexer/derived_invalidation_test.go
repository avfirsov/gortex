package indexer

import "testing"

func TestDerivedPlanForBodyOnlyDelta(t *testing.T) {
	fingerprints := derivedFingerprints{
		declarations: "decl",
		imports:      "imports",
		runtime:      "runtime",
		artifacts:    "artifacts",
	}

	plan := derivedPlanForDelta(fingerprints, fingerprints, true, "gortex/internal/indexer/example.go", nil, nil)
	if plan.Flags != 0 {
		t.Fatalf("body-only delta flags = %v, want 0", plan.Flags)
	}
	if plan.BodyOnlyFiles != 1 {
		t.Fatalf("body-only files = %d, want 1", plan.BodyOnlyFiles)
	}
	if len(plan.Files) != 1 || plan.Files[0] != "gortex/internal/indexer/example.go" {
		t.Fatalf("files = %v, want exact changed file", plan.Files)
	}
	if plan.LegacyFallback {
		t.Fatal("body-only delta must not request legacy fallback")
	}
}
