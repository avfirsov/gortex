package lsp

import "testing"

func TestPhaseBreakerTripsOnlyOnUnbrokenZeroYieldStreak(t *testing.T) {
	b := newPhaseBreaker(3, nil, "targeted", "repo")
	b.observe(false)
	b.observe(false)
	if b.isTripped() {
		t.Fatal("tripped below the streak limit")
	}
	b.observe(false)
	if !b.isTripped() {
		t.Fatal("did not trip at the streak limit with zero successes")
	}
}

func TestPhaseBreakerAnySuccessDisarmsPermanently(t *testing.T) {
	b := newPhaseBreaker(3, nil, "hover", "repo")
	b.observe(false)
	b.observe(true) // the server answered once — it works for this workspace
	for i := 0; i < 100; i++ {
		b.observe(false)
	}
	if b.isTripped() {
		t.Fatal("breaker tripped after a successful answer; it may only abandon zero-yield phases")
	}
}

func TestPhaseBreakerDisabled(t *testing.T) {
	b := newPhaseBreaker(0, nil, "targeted", "repo")
	for i := 0; i < 100; i++ {
		b.observe(false)
	}
	if b.isTripped() {
		t.Fatal("limit 0 must never trip")
	}
	var nilBreaker *phaseBreaker
	nilBreaker.observe(false) // must not panic
	if nilBreaker.isTripped() {
		t.Fatal("nil breaker reports tripped")
	}
}

func TestLSPPhaseFailureStreakLimitEnv(t *testing.T) {
	t.Setenv("GORTEX_LSP_BREAKER", "")
	if got := lspPhaseFailureStreakLimit(); got != defaultLSPPhaseFailureStreak {
		t.Fatalf("default limit = %d, want %d", got, defaultLSPPhaseFailureStreak)
	}
	t.Setenv("GORTEX_LSP_BREAKER", "off")
	if got := lspPhaseFailureStreakLimit(); got != 0 {
		t.Fatalf("off limit = %d, want 0", got)
	}
	t.Setenv("GORTEX_LSP_BREAKER", "7")
	if got := lspPhaseFailureStreakLimit(); got != 7 {
		t.Fatalf("override limit = %d, want 7", got)
	}
	t.Setenv("GORTEX_LSP_BREAKER", "banana")
	if got := lspPhaseFailureStreakLimit(); got != defaultLSPPhaseFailureStreak {
		t.Fatalf("malformed limit = %d, want default", got)
	}
}

func TestLSPProductivityWindowEnv(t *testing.T) {
	t.Setenv("GORTEX_LSP_PRODUCTIVITY_WINDOW", "")
	if got := lspProductivityWindow(); got != defaultLSPProductivityWindow {
		t.Fatalf("default window = %v, want %v", got, defaultLSPProductivityWindow)
	}
	t.Setenv("GORTEX_LSP_PRODUCTIVITY_WINDOW", "off")
	if got := lspProductivityWindow(); got != 0 {
		t.Fatalf("off window = %v, want 0", got)
	}
	t.Setenv("GORTEX_LSP_PRODUCTIVITY_WINDOW", "45s")
	if got := lspProductivityWindow(); got.Seconds() != 45 {
		t.Fatalf("override window = %v, want 45s", got)
	}
}

func TestRequestStatsTotalSumsIssuedRequests(t *testing.T) {
	var s requestStats
	s.references.Add(3)
	s.hovers.Add(5)
	s.subtypes.Add(1)
	// incomingSkipped is a skip counter, not an issued request; it must not
	// count as evidence of flowing volume.
	s.incomingSkipped.Add(100)
	if got := s.total(); got != 9 {
		t.Fatalf("total = %d, want 9", got)
	}
}
