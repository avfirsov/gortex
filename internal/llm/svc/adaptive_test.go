package svc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/llm"
)

// --- completeChunked unit tests --------------------------------------

// overflowAbove returns a call func that "overflows" (returns a
// context-overflow error) whenever the chunk holds more than limit
// items, and otherwise returns the chunk verbatim as its result.
func overflowAbove(limit int, calls *int) func([]int) ([]int, error) {
	return func(sub []int) ([]int, error) {
		*calls++
		if len(sub) > limit {
			return nil, errors.New("fake: maximum context length exceeded")
		}
		return sub, nil
	}
}

func concatInts(parts [][]int) []int {
	var out []int
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func TestCompleteChunked_NoOverflow(t *testing.T) {
	calls := 0
	got, chunked, err := completeChunked([]int{1, 2, 3}, overflowAbove(100, &calls), concatInts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chunked {
		t.Error("chunked should be false when the first call succeeds")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (no bisection)", calls)
	}
	if fmt.Sprint(got) != fmt.Sprint([]int{1, 2, 3}) {
		t.Errorf("got %v, want [1 2 3]", got)
	}
}

func TestCompleteChunked_BisectsOnOverflow(t *testing.T) {
	calls := 0
	cands := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	got, chunked, err := completeChunked(cands, overflowAbove(3, &calls), concatInts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !chunked {
		t.Error("chunked should be true after bisection")
	}
	// Bisection must preserve every candidate, in order.
	if fmt.Sprint(got) != fmt.Sprint(cands) {
		t.Errorf("got %v, want %v — bisection must preserve order and membership", got, cands)
	}
}

func TestCompleteChunked_NonOverflowErrorAborts(t *testing.T) {
	calls := 0
	call := func(sub []int) ([]int, error) {
		calls++
		return nil, errors.New("fake: 401 invalid api key")
	}
	_, chunked, err := completeChunked([]int{1, 2, 3, 4}, call, concatInts)
	if err == nil {
		t.Fatal("expected the non-overflow error to be returned")
	}
	if chunked {
		t.Error("a non-overflow error must not trigger bisection")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 — non-overflow error aborts immediately", calls)
	}
}

func TestCompleteChunked_SucceedsAtEightLeaves(t *testing.T) {
	calls := 0
	// 8 items, overflow above 1 → must bisect into 8 single-item leaves.
	cands := []int{0, 1, 2, 3, 4, 5, 6, 7}
	got, chunked, err := completeChunked(cands, overflowAbove(1, &calls), concatInts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !chunked {
		t.Error("chunked should be true")
	}
	if fmt.Sprint(got) != fmt.Sprint(cands) {
		t.Errorf("got %v, want %v", got, cands)
	}
}

func TestCompleteChunked_FailsWhenLeafStillOverflows(t *testing.T) {
	calls := 0
	// 16 items, overflow above 1: the deepest allowed leaf still holds
	// 2 items (16 -> 8 -> 4 -> 2), so the 8x ceiling is hit and the
	// overflow is surfaced as a hard error.
	cands := make([]int, 16)
	_, chunked, err := completeChunked(cands, overflowAbove(1, &calls), concatInts)
	if err == nil {
		t.Fatal("expected a hard error once the 8x bisection ceiling is exhausted")
	}
	if !llm.IsContextOverflow(err) {
		t.Errorf("surfaced error should still read as a context overflow: %v", err)
	}
	if chunked {
		t.Error("chunked must be false when bisection ultimately fails")
	}
}

func TestConcatDedupe(t *testing.T) {
	got := concatDedupe([][]string{{"a", "b"}, {"b", "c"}, {"c", "d"}})
	if strings.Join(got, ",") != "a,b,c,d" {
		t.Errorf("concatDedupe = %v, want [a b c d]", got)
	}
}

// --- integration: adaptive chunking through the assist passes --------

// fakeOverflowProvider implements llm.Provider. It counts the "id-"
// markers in the prompt and fails with a context-overflow error when
// that exceeds maxCands; otherwise it echoes every id back under the
// JSON key the request's shape expects.
type fakeOverflowProvider struct {
	maxCands int
	calls    int
}

func (p *fakeOverflowProvider) Name() string { return "fake" }
func (p *fakeOverflowProvider) Close() error { return nil }

func (p *fakeOverflowProvider) Complete(_ context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
	p.calls++
	var user string
	for _, m := range req.Messages {
		if m.Role == llm.RoleUser {
			user = m.Content
		}
	}
	if strings.Count(user, "id-") > p.maxCands {
		return llm.CompletionResponse{}, errors.New("fake: prompt is too long for this model's context window")
	}
	ids := scanFakeIDs(user)
	key := ""
	switch req.Shape {
	case llm.ShapeRerankOrder:
		key = "order"
	case llm.ShapeVerifyKeep:
		key = "keep"
	default:
		return llm.CompletionResponse{Text: "ok"}, nil
	}
	b, _ := json.Marshal(map[string][]string{key: ids})
	return llm.CompletionResponse{Text: string(b)}, nil
}

// scanFakeIDs pulls every whitespace-delimited "id-N" token out of a
// prompt, in first-seen order without duplicates.
func scanFakeIDs(prompt string) []string {
	seen := map[string]bool{}
	var out []string
	for tok := range strings.FieldsSeq(prompt) {
		if strings.HasPrefix(tok, "id-") && !seen[tok] {
			seen[tok] = true
			out = append(out, tok)
		}
	}
	return out
}

func newFakeService(p llm.Provider) *Service {
	s := NewService(llm.Config{}, llm.MockBackend{})
	s.provider = p
	return s
}

func TestRerankSymbols_AdaptiveChunking(t *testing.T) {
	cands := make([]llm.RerankCandidate, 20)
	for i := range cands {
		cands[i] = llm.RerankCandidate{ID: fmt.Sprintf("id-%d", i), Name: fmt.Sprintf("name%d", i)}
	}
	// maxCands 5 → the full set of 20 overflows; bisection must reach
	// chunks of 5 before the fake provider accepts the prompt.
	prov := &fakeOverflowProvider{maxCands: 5}
	s := newFakeService(prov)

	res, err := s.RerankSymbols(context.Background(), "find the thing", cands)
	if err != nil {
		t.Fatalf("RerankSymbols: %v", err)
	}
	if !res.Chunked {
		t.Error("expected Chunked=true — the 20-candidate prompt overflows")
	}
	if len(res.Order) != 20 {
		t.Fatalf("Order has %d ids, want 20 — bisection must preserve every candidate", len(res.Order))
	}
	seen := map[string]bool{}
	for _, id := range res.Order {
		seen[id] = true
	}
	for _, c := range cands {
		if !seen[c.ID] {
			t.Errorf("candidate %s lost during chunked rerank", c.ID)
		}
	}
}

func TestVerifyRelevance_AdaptiveChunking(t *testing.T) {
	cands := make([]llm.VerifyCandidate, 20)
	for i := range cands {
		cands[i] = llm.VerifyCandidate{ID: fmt.Sprintf("id-%d", i), Name: fmt.Sprintf("name%d", i)}
	}
	prov := &fakeOverflowProvider{maxCands: 5}
	s := newFakeService(prov)

	res, err := s.VerifyRelevance(context.Background(), "is this relevant", cands)
	if err != nil {
		t.Fatalf("VerifyRelevance: %v", err)
	}
	if !res.Chunked {
		t.Error("expected Chunked=true — the 20-candidate prompt overflows")
	}
	// The fake keeps every id, so chunked verification must return all 20.
	if len(res.Keep) != 20 {
		t.Fatalf("Keep has %d ids, want 20 — chunked verify must be exact", len(res.Keep))
	}
}

func TestRerankSymbols_NoChunkingWhenItFits(t *testing.T) {
	cands := []llm.RerankCandidate{
		{ID: "id-0", Name: "a"}, {ID: "id-1", Name: "b"}, {ID: "id-2", Name: "c"},
	}
	prov := &fakeOverflowProvider{maxCands: 50}
	s := newFakeService(prov)

	res, err := s.RerankSymbols(context.Background(), "q", cands)
	if err != nil {
		t.Fatalf("RerankSymbols: %v", err)
	}
	if res.Chunked {
		t.Error("Chunked should be false when the prompt fits")
	}
	if prov.calls != 1 {
		t.Errorf("provider calls = %d, want 1 (no bisection)", prov.calls)
	}
}
