package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/zzet/gortex/internal/tokens"
)

// testModel is a Claude model id used across the offline-counter
// tests; it resolves to the "claude" tokenizer family.
const testModel = "claude-opus-4-20250514"

func TestModelCounter_UsesPerModelTokenizer(t *testing.T) {
	const payload = "func hello() { return \"world\" }"
	c := newModelCounter(testModel)
	got, exact := c.Count("case", "json", payload, 0)
	if want := tokens.CountFor(testModel, payload); got != want {
		t.Errorf("modelCounter.Count = %d, want %d (tokens.CountFor)", got, want)
	}
	if exact {
		t.Error("modelCounter.Count must always report exact=false")
	}
}

func TestModelCounter_EmptyModelDefaults(t *testing.T) {
	if c := newModelCounter(""); c.model != defaultOpus47Model {
		t.Errorf("empty model = %q, want default %q", c.model, defaultOpus47Model)
	}
}

func TestModelCounter_VariesWithPayload(t *testing.T) {
	// A real tokenizer estimate must track the payload, not return a
	// flat scalar — short and long inputs differ.
	c := newModelCounter(testModel)
	short, _ := c.Count("s", "json", "x", 0)
	long, _ := c.Count("l", "json", "the quick brown fox jumps over the lazy dog", 0)
	if long <= short {
		t.Errorf("longer payload must score higher: short=%d long=%d", short, long)
	}
}

func TestCachedCounter_HitsCacheReturnsExact(t *testing.T) {
	c := newCachedCounter(opus47Cache{
		"case_a": {JSON: 200, GCX: 150},
	}, testModel)
	if got, exact := c.Count("case_a", "json", "ignored", 999); got != 200 || !exact {
		t.Errorf("cache hit (json) = (%d, %v), want (200, true)", got, exact)
	}
	if got, exact := c.Count("case_a", "gcx", "ignored", 999); got != 150 || !exact {
		t.Errorf("cache hit (gcx) = (%d, %v), want (150, true)", got, exact)
	}
}

func TestCachedCounter_MissFallsBackToModelCounter(t *testing.T) {
	const payload = "some representative fixture payload"
	c := newCachedCounter(opus47Cache{}, testModel)
	got, exact := c.Count("unknown", "json", payload, 100)
	if want := tokens.CountFor(testModel, payload); got != want || exact {
		t.Errorf("cache miss = (%d, %v), want (%d, false)", got, exact, want)
	}
}

func TestCachedCounter_PartialEntryFallsBackToModelCounter(t *testing.T) {
	// Entry exists but only one channel populated → the other channel
	// must fall through to the model counter so a partial cache stays
	// useful.
	const payload = "gcx channel payload"
	c := newCachedCounter(opus47Cache{
		"half": {JSON: 200, GCX: 0},
	}, testModel)
	got, exact := c.Count("half", "gcx", payload, 100)
	if want := tokens.CountFor(testModel, payload); got != want || exact {
		t.Errorf("partial cache (gcx empty) = (%d, %v), want (%d, false)", got, exact, want)
	}
	got, exact = c.Count("half", "json", "ignored", 100)
	if got != 200 || !exact {
		t.Errorf("partial cache (json populated) = (%d, %v), want (200, true)", got, exact)
	}
}

func TestOpus47Cache_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opus47-counts.json")

	want := opus47Cache{
		"case_a": {JSON: 100, GCX: 80},
		"case_b": {JSON: 50, GCX: 40},
	}
	if err := saveOpus47Cache(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := loadOpus47Cache(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 || got["case_a"].JSON != 100 || got["case_b"].GCX != 40 {
		t.Errorf("round-trip lost data: %+v", got)
	}
}

func TestLoadOpus47Cache_MissingFileReturnsEmpty(t *testing.T) {
	got, err := loadOpus47Cache(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Errorf("missing file should not error, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("missing file = %d entries, want 0", len(got))
	}
}

func TestLoadOpus47Cache_EmptyFileReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(path, []byte("   \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadOpus47Cache(path)
	if err != nil {
		t.Errorf("empty file should not error, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty file = %d entries, want 0", len(got))
	}
}

func TestNewAPICounter_MissingKeyRejects(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	if _, err := newAPICounter(newCachedCounter(nil, testModel), "claude-opus-4"); err == nil {
		t.Fatal("expected error when ANTHROPIC_API_KEY is empty")
	}
}

func TestAPICounter_CallsAndCaches(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Errorf("missing x-api-key header, got %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got != opus47AnthropicVersion {
			t.Errorf("wrong anthropic-version header, got %q", got)
		}
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int{"input_tokens": 250})
	}))
	defer srv.Close()

	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	cached := newCachedCounter(nil, testModel)
	api, err := newAPICounter(cached, "claude-opus-4-test")
	if err != nil {
		t.Fatal(err)
	}
	api.apiBase = srv.URL

	got, exact := api.Count("case_x", "json", "the payload", 100)
	if got != 250 || !exact {
		t.Errorf("first API call = (%d, %v), want (250, true)", got, exact)
	}
	if hits.Load() != 1 {
		t.Errorf("expected 1 API hit, got %d", hits.Load())
	}

	// Second call on the same (case, channel) must use the cache, not the API.
	got, exact = api.Count("case_x", "json", "the payload", 100)
	if got != 250 || !exact {
		t.Errorf("cached API call = (%d, %v), want (250, true)", got, exact)
	}
	if hits.Load() != 1 {
		t.Errorf("expected still 1 API hit (cache should serve), got %d", hits.Load())
	}
}

func TestAPICounter_ErrorFallsBackToModelCounter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	cached := newCachedCounter(nil, testModel)
	api, err := newAPICounter(cached, "claude-opus-4-test")
	if err != nil {
		t.Fatal(err)
	}
	api.apiBase = srv.URL

	const payload = "the payload that the API refused to count"
	got, exact := api.Count("case_y", "json", payload, 100)
	if want := tokens.CountFor(testModel, payload); got != want || exact {
		t.Errorf("API error → fallback = (%d, %v), want (%d, false)", got, exact, want)
	}
}

func TestParseTokenizerMode(t *testing.T) {
	cases := map[string]tokenizerMode{
		"cl100k":      tokenizerModeCL100k,
		"cl100k_base": tokenizerModeCL100k,
		"opus47":      tokenizerModeOpus47,
		"opus4.7":     tokenizerModeOpus47,
		"opus-4-7":    tokenizerModeOpus47,
		"claude":      tokenizerModeOpus47,
		"both":        tokenizerModeBoth,
		"all":         tokenizerModeBoth,
		"CL100K":      tokenizerModeCL100k, // case-insensitive
	}
	for in, want := range cases {
		got, err := parseTokenizerMode(in)
		if err != nil {
			t.Errorf("parseTokenizerMode(%q) errored: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseTokenizerMode(%q) = %d, want %d", in, got, want)
		}
	}
	if _, err := parseTokenizerMode("bogus"); err == nil {
		t.Error("expected error for unknown tokenizer name")
	}
}

func TestBuildOpus47Counter_NoCacheUsesModelCounter(t *testing.T) {
	counter, cache, err := buildOpus47Counter("", "", false)
	if err != nil {
		t.Fatalf("buildOpus47Counter: %v", err)
	}
	if cache != nil {
		t.Error("no-cache path should return nil underlying cache")
	}
	const payload = "no-cache fixture payload"
	if got, exact := counter.Count("c", "json", payload, 100); got != tokens.CountFor(defaultOpus47Model, payload) || exact {
		t.Errorf("no-cache counter = (%d, %v), want (%d, false) — in-process model counter",
			got, exact, tokens.CountFor(defaultOpus47Model, payload))
	}
}
