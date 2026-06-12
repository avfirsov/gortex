package ollama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/llm"
)

func TestNew_MissingModel(t *testing.T) {
	if _, err := New(llm.OllamaConfig{}); err == nil {
		t.Fatal("expected error when model is unset")
	}
}

func TestNew_DefaultsHost(t *testing.T) {
	p, err := New(llm.OllamaConfig{Model: "qwen"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	if p.Name() != "ollama" {
		t.Errorf("Name()=%q", p.Name())
	}
}

func TestComplete_StructuredSendsFormatSchema(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path=%q want /api/chat", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = io.WriteString(w, `{"message":{"role":"assistant","content":"{\"keep\":[\"a\"]}"}}`)
	}))
	defer srv.Close()

	p, err := New(llm.OllamaConfig{Model: "qwen2.5-coder:7b", Host: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()

	resp, err := p.Complete(context.Background(), llm.CompletionRequest{
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: "verify"}},
		Shape:     llm.ShapeVerifyKeep,
		MaxTokens: 128,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != `{"keep":["a"]}` {
		t.Errorf("text=%q", resp.Text)
	}
	if gotBody["format"] == nil {
		t.Error("structured request must send a `format` schema")
	}
	if gotBody["stream"] != false {
		t.Errorf("stream=%v want false", gotBody["stream"])
	}
	opts, _ := gotBody["options"].(map[string]any)
	if opts == nil || opts["num_predict"] == nil {
		t.Errorf("options.num_predict missing: %v", gotBody["options"])
	}
}

func TestComplete_FreeformNoFormat(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = io.WriteString(w, `{"message":{"role":"assistant","content":"hi there"}}`)
	}))
	defer srv.Close()

	p, _ := New(llm.OllamaConfig{Model: "m", Host: srv.URL})
	defer func() { _ = p.Close() }()

	resp, err := p.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		Shape:    llm.ShapeFreeform,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "hi there" {
		t.Errorf("text=%q", resp.Text)
	}
	if _, ok := gotBody["format"]; ok {
		t.Error("freeform request must not send a `format` field")
	}
}

func TestComplete_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"model not found"}`)
	}))
	defer srv.Close()

	p, _ := New(llm.OllamaConfig{Model: "m", Host: srv.URL})
	defer func() { _ = p.Close() }()

	if _, err := p.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	}); err == nil {
		t.Fatal("expected an error for a non-200 response")
	}
}

func TestComplete_InlineErrorField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 200 OK but an error payload — Ollama does this for some failures.
		_, _ = io.WriteString(w, `{"error":"something went wrong"}`)
	}))
	defer srv.Close()

	p, _ := New(llm.OllamaConfig{Model: "m", Host: srv.URL})
	defer func() { _ = p.Close() }()

	if _, err := p.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	}); err == nil {
		t.Fatal("expected an error when the response carries an inline error field")
	}
}

// TestDeriveNumCtx_ClampsSmallPromptToFloor proves a tiny request does
// not get a window narrower than Ollama's own default — a small prompt
// must not be handed a degenerate context window.
func TestDeriveNumCtx_ClampsSmallPromptToFloor(t *testing.T) {
	got := deriveNumCtx(llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if got != minNumCtx {
		t.Errorf("num_ctx=%d want the %d floor for a tiny prompt", got, minNumCtx)
	}
}

// TestDeriveNumCtx_ScalesWithPromptSize proves the window grows with
// the prompt: a large prompt yields a strictly larger num_ctx than a
// small one, so a big chunk is not silently truncated by a fixed
// default.
func TestDeriveNumCtx_ScalesWithPromptSize(t *testing.T) {
	small := deriveNumCtx(llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "short question"}},
	})
	// ~80k characters of prompt — well past the 2048 floor once
	// converted to tokens, but still under the ceiling.
	big := deriveNumCtx(llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: strings.Repeat("word ", 16000)}},
	})
	if big <= small {
		t.Errorf("num_ctx for a big prompt (%d) must exceed a small one (%d)", big, small)
	}
	if big <= minNumCtx {
		t.Errorf("a large prompt must lift num_ctx above the floor, got %d", big)
	}
	if big > maxNumCtx {
		t.Errorf("num_ctx=%d must not exceed the ceiling %d", big, maxNumCtx)
	}
	if big%numCtxBlock != 0 {
		t.Errorf("num_ctx=%d must be rounded to a multiple of %d", big, numCtxBlock)
	}
}

// TestDeriveNumCtx_ClampsHugePromptToCeiling proves an oversized prompt
// is capped — num_ctx never grows without bound, which would risk an
// out-of-memory abort on the Ollama host.
func TestDeriveNumCtx_ClampsHugePromptToCeiling(t *testing.T) {
	got := deriveNumCtx(llm.CompletionRequest{
		// ~600k characters — far beyond any sane window.
		Messages: []llm.Message{{Role: llm.RoleUser, Content: strings.Repeat("token ", 100000)}},
	})
	if got != maxNumCtx {
		t.Errorf("num_ctx=%d want the %d ceiling for an oversized prompt", got, maxNumCtx)
	}
}

// TestDeriveNumCtx_ReservesGenerationBudget proves the reply's token
// budget is folded into the window: a request with a large MaxTokens
// gets a wider num_ctx than the same prompt with none.
func TestDeriveNumCtx_ReservesGenerationBudget(t *testing.T) {
	msgs := []llm.Message{{Role: llm.RoleUser, Content: strings.Repeat("word ", 4000)}}
	withoutBudget := deriveNumCtx(llm.CompletionRequest{Messages: msgs})
	withBudget := deriveNumCtx(llm.CompletionRequest{Messages: msgs, MaxTokens: 8000})
	if withBudget <= withoutBudget {
		t.Errorf("a large MaxTokens must widen num_ctx: with=%d without=%d", withBudget, withoutBudget)
	}
}

// TestComplete_SendsDerivedNumCtx proves the request actually carries
// the derived num_ctx in its options block.
func TestComplete_SendsDerivedNumCtx(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = io.WriteString(w, `{"message":{"role":"assistant","content":"ok"}}`)
	}))
	defer srv.Close()

	p, _ := New(llm.OllamaConfig{Model: "m", Host: srv.URL})
	defer func() { _ = p.Close() }()

	if _, err := p.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	}); err != nil {
		t.Fatal(err)
	}
	opts, _ := gotBody["options"].(map[string]any)
	if opts == nil {
		t.Fatalf("request carried no options block: %v", gotBody)
	}
	numCtx, ok := opts["num_ctx"].(float64)
	if !ok {
		t.Fatalf("options.num_ctx missing or non-numeric: %v", opts["num_ctx"])
	}
	if int(numCtx) != minNumCtx {
		t.Errorf("num_ctx=%v want %d (a tiny prompt clamps to the floor)", numCtx, minNumCtx)
	}
}

// TestComplete_HollowResponseRetriesThenSucceeds proves an HTTP 200
// whose message content is empty is treated as a transient truncation:
// the first hollow answer is retried and the second, good answer wins.
func TestComplete_HollowResponseRetriesThenSucceeds(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			_, _ = io.WriteString(w, `{"message":{"role":"assistant","content":""}}`)
			return
		}
		_, _ = io.WriteString(w, `{"message":{"role":"assistant","content":"recovered"}}`)
	}))
	defer srv.Close()

	p, _ := New(llm.OllamaConfig{Model: "m", Host: srv.URL})
	defer func() { _ = p.Close() }()

	resp, err := p.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "recovered" {
		t.Errorf("text=%q want %q", resp.Text, "recovered")
	}
	if calls != 2 {
		t.Errorf("calls=%d want 2 (one hollow 200, then a retry)", calls)
	}
}
