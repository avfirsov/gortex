package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/zzet/gortex/internal/tokens"
)

// opus47Counter measures the input-token cost of a payload against the
// Claude Opus 4.7 tokenizer. We use three strategies, picked at flag
// time, that share this interface:
//
//   - modelCounter:  an in-process, per-model tiktoken estimate via
//     internal/tokens.CountFor (default). Offline, deterministic, and
//     tokenizer-aware — it counts with the encoding the model family
//     actually uses and applies the family calibration ratio, so the
//     estimate tracks each fixture instead of a single flat scalar.
//   - cachedCounter:  reads pre-computed exact counts from a JSON
//     sidecar on disk. Falls back to the model counter when the cache
//     misses, so a partial cache is still useful.
//   - apiCounter:     calls Anthropic's `messages/count_tokens`
//     endpoint with the configured model id; populates the cache on
//     success so subsequent runs are deterministic. Requires
//     ANTHROPIC_API_KEY in the environment.
//
// Returning `exact=true` lets the scorecard label each row as
// estimated or exact — important for the published artifact to be
// honest about which numbers came from where.
type opus47Counter interface {
	Count(caseName, channel, payload string, cl100k int) (count int, exact bool)
}

// defaultOpus47Model is the model id the offline counter resolves its
// tokenizer family from when --opus47-model is left unset.
const defaultOpus47Model = "claude-opus-4-20250514"

// --- model counter ---------------------------------------------------

// modelCounter estimates input tokens with internal/tokens.CountFor —
// the per-model, tokenizer-aware estimator. A Claude model id resolves
// to cl100k_base scaled by the empirically calibrated Claude ratio;
// because it tokenizes the actual payload, the estimate varies
// per-fixture rather than applying one uniform scalar. The cheapest
// strategy: pure in-process compute, no I/O, no network.
type modelCounter struct{ model string }

func newModelCounter(model string) modelCounter {
	if model == "" {
		model = defaultOpus47Model
	}
	return modelCounter{model: model}
}

func (c modelCounter) Count(_, _, payload string, _ int) (int, bool) {
	return tokens.CountFor(c.model, payload), false
}

// --- cached counter --------------------------------------------------

// opus47CacheEntry records exact counts for one fixture's two channels.
// JSON keys mirror the encoder names so the on-disk file is easy to
// edit by hand.
type opus47CacheEntry struct {
	JSON int `json:"json"`
	GCX  int `json:"gcx"`
}

// opus47Cache is the on-disk shape of `opus47-counts.json` — a map
// keyed by case name. Missing entries are tolerated: the cached
// counter falls through to the model counter and the API counter
// fills the gap on the next `--use-api` run.
type opus47Cache map[string]opus47CacheEntry

// loadOpus47Cache reads the cache from disk. Returns an empty cache
// (not an error) when the file is missing; surfaces real I/O errors
// so the harness fails loud on permission / disk problems.
func loadOpus47Cache(path string) (opus47Cache, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return opus47Cache{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read opus47 cache %s: %w", path, err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return opus47Cache{}, nil
	}
	var c opus47Cache
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse opus47 cache %s: %w", path, err)
	}
	return c, nil
}

// saveOpus47Cache writes the cache atomically (tmp+rename) so a
// crash mid-flush doesn't corrupt the file.
func saveOpus47Cache(path string, c opus47Cache) error {
	tmp := path + ".tmp"
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// cachedCounter consults the cache first, falls through to the model
// counter on miss. Concurrent reads are safe; concurrent writes (via
// the API counter) are serialized through the mutex.
type cachedCounter struct {
	mu       sync.RWMutex
	cache    opus47Cache
	fallback modelCounter
}

func newCachedCounter(c opus47Cache, model string) *cachedCounter {
	if c == nil {
		c = opus47Cache{}
	}
	return &cachedCounter{cache: c, fallback: newModelCounter(model)}
}

func (c *cachedCounter) Count(caseName, channel, payload string, cl100k int) (int, bool) {
	c.mu.RLock()
	entry, ok := c.cache[caseName]
	c.mu.RUnlock()
	if ok {
		switch channel {
		case "json":
			if entry.JSON > 0 {
				return entry.JSON, true
			}
		case "gcx":
			if entry.GCX > 0 {
				return entry.GCX, true
			}
		}
	}
	return c.fallback.Count(caseName, channel, payload, cl100k)
}

func (c *cachedCounter) snapshot() opus47Cache {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(opus47Cache, len(c.cache))
	maps.Copy(out, c.cache)
	return out
}

func (c *cachedCounter) store(caseName, channel string, count int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.cache[caseName]
	switch channel {
	case "json":
		entry.JSON = count
	case "gcx":
		entry.GCX = count
	}
	c.cache[caseName] = entry
}

// --- API counter -----------------------------------------------------

// apiCounter wraps a cachedCounter and falls through to Anthropic's
// `messages/count_tokens` endpoint on cache miss, then stores the
// result for future runs. Network failures degrade to the model
// counter with a warning on stderr — the harness must keep running
// when the user is offline.
type apiCounter struct {
	cached  *cachedCounter
	client  *http.Client
	apiKey  string
	model   string
	apiBase string
	warned  sync.Once
}

// opus47APIEndpoint is the documented Anthropic counter endpoint.
const opus47APIEndpoint = "https://api.anthropic.com/v1/messages/count_tokens"

// opus47AnthropicVersion is the API header required for the
// count_tokens endpoint. Bumping requires verifying the response
// schema still has `input_tokens`.
const opus47AnthropicVersion = "2023-06-01"

func newAPICounter(cached *cachedCounter, model string) (*apiCounter, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return nil, errors.New("--use-api requires ANTHROPIC_API_KEY in environment")
	}
	if model == "" {
		model = defaultOpus47Model
	}
	return &apiCounter{
		cached:  cached,
		client:  &http.Client{Timeout: 30 * time.Second},
		apiKey:  apiKey,
		model:   model,
		apiBase: opus47APIEndpoint,
	}, nil
}

func (a *apiCounter) Count(caseName, channel, payload string, cl100k int) (int, bool) {
	if got, ok := a.cached.Count(caseName, channel, payload, cl100k); ok {
		return got, true
	}
	count, err := a.callAPI(payload)
	if err != nil {
		// Fail soft: warn once, keep ticking with the model counter.
		a.warned.Do(func() {
			fmt.Fprintf(os.Stderr, "wire-bench: opus47 API counter degraded to in-process model counter after first error: %v\n", err)
		})
		return a.cached.fallback.Count(caseName, channel, payload, cl100k)
	}
	a.cached.store(caseName, channel, count)
	return count, true
}

// apiResponse mirrors the documented response shape; we only care
// about input_tokens but parse strictly so a schema drift surfaces.
type apiResponse struct {
	InputTokens int `json:"input_tokens"`
}

// callAPI POSTs the payload as a single user message and returns the
// integer input-token count. The chat-wrapper overhead (~3-5 tokens
// for the role+system framing) is part of the answer — documenting
// that in the scorecard footnote rather than trying to subtract it
// keeps the harness honest about exactly what it measured.
func (a *apiCounter) callAPI(payload string) (int, error) {
	body, _ := json.Marshal(map[string]any{
		"model": a.model,
		"messages": []map[string]string{
			{"role": "user", "content": payload},
		},
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, a.apiBase, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", opus47AnthropicVersion)
	req.Header.Set("content-type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("opus47 API %d: %s", resp.StatusCode, string(raw))
	}
	var r apiResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return 0, fmt.Errorf("opus47 API parse: %w", err)
	}
	if r.InputTokens <= 0 {
		return 0, fmt.Errorf("opus47 API returned non-positive count: %s", string(raw))
	}
	return r.InputTokens, nil
}
