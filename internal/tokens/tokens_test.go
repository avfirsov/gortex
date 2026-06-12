package tokens

import (
	"strings"
	"testing"
)

func TestCount_EmptyString(t *testing.T) {
	if got := Count(""); got != 0 {
		t.Errorf("Count(\"\") = %d, want 0", got)
	}
}

func TestCount_SimpleEnglish(t *testing.T) {
	// "Hello, world!" is 4 tokens in cl100k_base: "Hello", ",", " world", "!".
	got := Count("Hello, world!")
	if got < 3 || got > 5 {
		t.Errorf("Count(\"Hello, world!\") = %d, want approximately 4", got)
	}
}

func TestCount_CodeVsProseDiffersFromHeuristic(t *testing.T) {
	// Code tokenizes denser than chars/4 — this is the whole reason for A8.
	// Pick a representative Go snippet.
	code := `func (s *Server) handleGetSymbolSource(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError("id is required"), nil
	}
	return nil, nil
}`
	real := Count(code)
	heuristic := len(code) / 4
	if real == 0 {
		t.Fatal("tiktoken returned 0 for non-empty code")
	}
	// We expect the real count to be larger than chars/4 for code — the
	// heuristic undercounts by 15-25% on typical Go.
	if real <= heuristic {
		t.Errorf("expected tiktoken count (%d) > chars/4 heuristic (%d) for code",
			real, heuristic)
	}
}

func TestCountInt64_ReturnsInt64OfCount(t *testing.T) {
	s := "alpha beta gamma"
	if got, want := CountInt64(s), int64(Count(s)); got != want {
		t.Errorf("CountInt64 = %d, Count = %d, expected equal", got, want)
	}
}

func TestTokensToChars_ZeroAndNegative(t *testing.T) {
	if got := TokensToChars(0); got != 0 {
		t.Errorf("TokensToChars(0) = %d, want 0", got)
	}
	if got := TokensToChars(-10); got != 0 {
		t.Errorf("TokensToChars(-10) = %d, want 0", got)
	}
}

func TestTokensToChars_Positive(t *testing.T) {
	// With a 3.2 chars/token ratio, 1000 tokens -> 3200 chars.
	if got, want := TokensToChars(1000), 3200; got != want {
		t.Errorf("TokensToChars(1000) = %d, want %d", got, want)
	}
}

func TestEstimateFromSample_ZeroTotal(t *testing.T) {
	if got := EstimateFromSample(0, "abc"); got != 0 {
		t.Errorf("EstimateFromSample(0, _) = %d, want 0", got)
	}
}

func TestEstimateFromSample_EmptySample(t *testing.T) {
	// Falls back to chars/4 when we have no calibration sample.
	if got, want := EstimateFromSample(400, ""), 100; got != want {
		t.Errorf("EstimateFromSample(400, \"\") = %d, want %d (chars/4 fallback)", got, want)
	}
}

func TestEstimateFromSample_ExtrapolatesFromSample(t *testing.T) {
	// If the sample is a prefix of the file, the estimate for the full file
	// should scale with the ratio sampleTokens/len(sample).
	sample := "package main\n\nfunc Hello() {}\n"
	sampleTokens := Count(sample)
	total := len(sample) * 10
	got := EstimateFromSample(total, sample)
	want := sampleTokens * 10
	// Allow off-by-one from integer division.
	if got < want-1 || got > want+1 {
		t.Errorf("EstimateFromSample(%d, sample) = %d, want %d", total, got, want)
	}
}

func TestEncoderReady(t *testing.T) {
	if !EncoderReady() {
		t.Error("EncoderReady() = false — offline loader should have initialized cl100k_base")
	}
}

func BenchmarkCount_SmallCode(b *testing.B) {
	src := strings.Repeat("func foo() { return bar() }\n", 10)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Count(src)
	}
}

func BenchmarkCount_LargeCode(b *testing.B) {
	src := strings.Repeat("func foo() { return bar() }\n", 1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Count(src)
	}
}
