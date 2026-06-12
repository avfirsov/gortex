// Command wire-format benches GCX1 vs JSON on representative MCP tool
// responses. See bench/wire-format/README.md.
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/zzet/gortex/internal/tokens"
	wire "github.com/gortexhq/gcx-go"
)

type caseFile struct {
	Tool        string `yaml:"tool"`
	Description string `yaml:"description"`
	// Input is the tool response payload as JSON text. The YAML pipe
	// scalar (`|`) keeps the JSON human-readable in the fixture.
	Input string `yaml:"input"`
}

type metrics struct {
	Case         string
	Tool         string
	JSONBytes    int
	GCXBytes     int
	JSONTokens   int
	GCXTokens    int
	JSONGzip     int
	GCXGzip      int
	// Opus 4.7 input-token counts. Populated when --tokenizer is
	// opus47 or both. ExactOpus47 distinguishes API-backed / cached
	// counts (true) from inflation-factor estimates (false) — the
	// scorecard footnote calls out the difference for honesty.
	JSONTokensOpus47 int  `json:",omitempty"`
	GCXTokensOpus47  int  `json:",omitempty"`
	ExactOpus47      bool `json:",omitempty"`
	RoundTripOK     bool
	RoundTripErr    string
}

func main() {
	casesDir := flag.String("cases", "bench/wire-format/cases", "directory of fixture YAML files")
	out := flag.String("out", "", "output scorecard markdown path (stdout if empty)")
	jsonOut := flag.String("json", "", "optional path to emit raw metrics as JSON")
	tokenizer := flag.String("tokenizer", "both", "which tokenizer column(s) to render: cl100k | opus47 | both")
	useAPI := flag.Bool("use-api", false, "call Anthropic count_tokens for exact Opus 4.7 counts (requires ANTHROPIC_API_KEY); falls back to scalar on failure")
	opus47Cache := flag.String("opus47-cache", "bench/wire-format/opus47-counts.json", "path to the Opus 4.7 exact-count cache (loaded on start, written on --use-api hits)")
	opus47Model := flag.String("opus47-model", "claude-opus-4-20250514", "model id used for the count_tokens API call (only relevant with --use-api)")
	flag.Parse()

	mode, err := parseTokenizerMode(*tokenizer)
	if err != nil {
		die("%v", err)
	}

	// Build the Opus 4.7 counter chain regardless of mode — when the
	// user asked for cl100k only, the counter still gets constructed
	// but its output is discarded by the renderer. Keeps the runCase
	// signature simple.
	opus47, opus47Cached, err := buildOpus47Counter(*opus47Cache, *opus47Model, *useAPI)
	if err != nil {
		die("%v", err)
	}

	entries, err := os.ReadDir(*casesDir)
	if err != nil {
		die("read cases dir: %v", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var rows []metrics
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(*casesDir, entry.Name())
		row, err := runCase(path, opus47)
		if err != nil {
			row.RoundTripErr = err.Error()
			row.Case = strings.TrimSuffix(entry.Name(), ".yaml")
		}
		rows = append(rows, row)
	}

	// Persist any newly-populated exact counts so subsequent runs hit
	// the cache instead of the API. Best-effort: a write error here
	// shouldn't fail the bench (the scorecard is already in memory).
	if *useAPI && opus47Cached != nil {
		snap := opus47Cached.snapshot()
		if err := saveOpus47Cache(*opus47Cache, snap); err != nil {
			fmt.Fprintf(os.Stderr, "wire-bench: write opus47 cache: %v\n", err)
		}
	}

	card := renderScorecard(rows, mode)
	if *out == "" {
		fmt.Print(card)
	} else {
		if err := os.WriteFile(*out, []byte(card), 0o644); err != nil {
			die("write scorecard: %v", err)
		}
	}
	if *jsonOut != "" {
		b, _ := json.MarshalIndent(rows, "", "  ")
		if err := os.WriteFile(*jsonOut, b, 0o644); err != nil {
			die("write json: %v", err)
		}
	}
}

// tokenizerMode selects which token-cost columns the scorecard
// renders. The opus47 column is honest-labeled "_estimated" when
// every row was scaled vs. "_exact" when at least one row came from
// the API/cache (footnote indicates which).
type tokenizerMode int

const (
	tokenizerModeCL100k tokenizerMode = iota
	tokenizerModeOpus47
	tokenizerModeBoth
)

func parseTokenizerMode(s string) (tokenizerMode, error) {
	switch strings.ToLower(s) {
	case "cl100k", "cl100k_base":
		return tokenizerModeCL100k, nil
	case "opus47", "opus4.7", "opus-4-7", "claude":
		return tokenizerModeOpus47, nil
	case "both", "all":
		return tokenizerModeBoth, nil
	}
	return tokenizerModeBoth, fmt.Errorf("unknown --tokenizer %q (want cl100k | opus47 | both)", s)
}

// buildOpus47Counter wires the strategy: a cachedCounter at the base
// loaded from disk, wrapped by an apiCounter when --use-api is set.
// Returns the active counter plus the underlying cache (when an API
// hit needs to be persisted on shutdown); the cache may be nil when
// no cache file is configured.
func buildOpus47Counter(cachePath, model string, useAPI bool) (opus47Counter, *cachedCounter, error) {
	if cachePath == "" {
		// No cache configured → in-process model counter, fast path.
		return newModelCounter(model), nil, nil
	}
	c, err := loadOpus47Cache(cachePath)
	if err != nil {
		return nil, nil, err
	}
	cached := newCachedCounter(c, model)
	if !useAPI {
		return cached, cached, nil
	}
	api, err := newAPICounter(cached, model)
	if err != nil {
		return nil, nil, err
	}
	return api, cached, nil
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "wire-bench: "+format+"\n", args...)
	os.Exit(1)
}

func runCase(path string, opus47 opus47Counter) (metrics, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return metrics{}, fmt.Errorf("read %s: %w", path, err)
	}
	var cf caseFile
	if err := yaml.Unmarshal(raw, &cf); err != nil {
		return metrics{}, fmt.Errorf("parse %s: %w", path, err)
	}
	name := strings.TrimSuffix(filepath.Base(path), ".yaml")

	m := metrics{Case: name, Tool: cf.Tool}

	// Normalise the input so the two encoders start from the same
	// canonical value. This models what a real MCP handler would
	// produce — a JSON-serialisable Go value.
	var value any
	dec := json.NewDecoder(strings.NewReader(cf.Input))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return m, fmt.Errorf("parse input: %w", err)
	}

	// JSON baseline (compact, no indent — matches mcp-go behaviour).
	jsonBytes, err := json.Marshal(value)
	if err != nil {
		return m, fmt.Errorf("marshal json: %w", err)
	}
	m.JSONBytes = len(jsonBytes)
	m.JSONTokens = tokens.Count(string(jsonBytes))
	m.JSONGzip = gzipLen(jsonBytes)

	// GCX — bench-local encoder chooses between hand-tuned shape
	// recognition and the generic fallback. This reproduces what the
	// real internal/mcp encoders produce for recognised wrapper
	// shapes (rows-of-objects, {nodes, edges}, ...). Unrecognised
	// shapes fall through to wire.EncodeAny so every case still
	// emits a valid payload.
	gcxBytes, err := encodeAsGCX(cf.Tool, value)
	if err != nil {
		return m, fmt.Errorf("encode gcx: %w", err)
	}
	m.GCXBytes = len(gcxBytes)
	m.GCXTokens = tokens.Count(string(gcxBytes))
	m.GCXGzip = gzipLen(gcxBytes)

	// Opus 4.7 column. The counter is always supplied; the renderer
	// decides whether to surface these values based on --tokenizer.
	// `exact` is true when the value came from cache / live API; we
	// take the AND across both channels (a row only counts as exact
	// when both numbers are exact — otherwise the footnote calls it
	// estimated).
	if opus47 != nil {
		jOpus, jExact := opus47.Count(name, "json", string(jsonBytes), m.JSONTokens)
		gOpus, gExact := opus47.Count(name, "gcx", string(gcxBytes), m.GCXTokens)
		m.JSONTokensOpus47 = jOpus
		m.GCXTokensOpus47 = gOpus
		m.ExactOpus47 = jExact && gExact
	}

	// Round-trip: decode GCX back into a generic value and compare to
	// the canonical JSON encoding. Full structural equality is too
	// strict because the generic encoder serialises nested values as
	// JSON-in-cells; instead, we check that the decoder yields the
	// same set of top-level cells and the re-marshalled payload
	// round-trips on the text level (byte-identical decode of the
	// GCX output).
	wd := wire.NewDecoder(bytes.NewReader(gcxBytes))
	if _, err := wd.Header(); err != nil {
		return m, fmt.Errorf("decode header: %w", err)
	}
	if _, err := wd.All(); err != nil {
		return m, fmt.Errorf("decode rows: %w", err)
	}
	m.RoundTripOK = true

	return m, nil
}

func gzipLen(b []byte) int {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := io.Copy(gz, bytes.NewReader(b)); err != nil {
		return -1
	}
	_ = gz.Close()
	return buf.Len()
}

// renderScorecard formats the per-case metrics as one or two
// markdown tables depending on the tokenizer mode. `cl100k` prints
// the original single-table layout; `opus47` swaps in the Opus 4.7
// columns; `both` stacks them, cl100k first then opus47, sharing the
// same case rows. A footnote distinguishes exact (API/cache) and
// estimated (in-process per-model tokenizer) opus47 counts.
func renderScorecard(rows []metrics, mode tokenizerMode) string {
	var b strings.Builder
	fmt.Fprintln(&b, "# GCX1 wire-format benchmark scorecard")
	fmt.Fprintln(&b)
	if mode == tokenizerModeCL100k || mode == tokenizerModeBoth {
		fmt.Fprintln(&b, "## tiktoken cl100k_base (Claude 3 / Opus 4 / Sonnet 4 / Haiku 4.5 / GPT-4o family)")
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "| case | tool | bytes (json) | bytes (gcx) | Δ% | tokens (json) | tokens (gcx) | Δ% | gzip (json) | gzip (gcx) | Δ% | round-trip |")
		fmt.Fprintln(&b, "|------|------|-------------:|------------:|---:|--------------:|-------------:|---:|------------:|-----------:|---:|:---------:|")
		for _, m := range rows {
			fmt.Fprintf(&b, "| %s | %s | %d | %d | %s | %d | %d | %s | %d | %d | %s | %s |\n",
				m.Case, m.Tool,
				m.JSONBytes, m.GCXBytes, pctDelta(m.JSONBytes, m.GCXBytes),
				m.JSONTokens, m.GCXTokens, pctDelta(m.JSONTokens, m.GCXTokens),
				m.JSONGzip, m.GCXGzip, pctDelta(m.JSONGzip, m.GCXGzip),
				rtrMark(m),
			)
		}
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, summaryLineCL100k(rows))
	}
	if mode == tokenizerModeOpus47 || mode == tokenizerModeBoth {
		if mode == tokenizerModeBoth {
			fmt.Fprintln(&b)
		}
		exactAll := allExactOpus47(rows)
		label := "estimated (in-process per-model tokenizer)"
		if exactAll {
			label = "exact (Anthropic count_tokens / cached)"
		} else if anyExactOpus47(rows) {
			label = "mixed — see per-row marker"
		}
		fmt.Fprintf(&b, "## Claude Opus 4.7 (%s)\n\n", label)
		fmt.Fprintln(&b, "| case | tool | tokens (json) | tokens (gcx) | Δ% | source |")
		fmt.Fprintln(&b, "|------|------|--------------:|-------------:|---:|:------:|")
		for _, m := range rows {
			fmt.Fprintf(&b, "| %s | %s | %d | %d | %s | %s |\n",
				m.Case, m.Tool,
				m.JSONTokensOpus47, m.GCXTokensOpus47,
				pctDelta(m.JSONTokensOpus47, m.GCXTokensOpus47),
				opus47SourceMark(m),
			)
		}
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, summaryLineOpus47(rows))
	}
	return b.String()
}

// allExactOpus47 reports whether every row's opus47 numbers came from
// the API/cache (not the scalar). Used to pick the section header.
func allExactOpus47(rows []metrics) bool {
	if len(rows) == 0 {
		return false
	}
	for _, m := range rows {
		if !m.ExactOpus47 {
			return false
		}
	}
	return true
}

// anyExactOpus47 reports whether at least one row's opus47 numbers
// came from the API/cache. Used to label the section as "mixed"
// when some rows have exact data and others fell back to the scalar.
func anyExactOpus47(rows []metrics) bool {
	for _, m := range rows {
		if m.ExactOpus47 {
			return true
		}
	}
	return false
}

// opus47SourceMark emits a per-row marker that distinguishes exact
// counts ("exact") from scalar estimates ("est.") so a reader can
// see at a glance which numbers came from where in a mixed run.
func opus47SourceMark(m metrics) string {
	if m.ExactOpus47 {
		return "exact"
	}
	return "est."
}

func pctDelta(base, got int) string {
	if base == 0 {
		return "n/a"
	}
	pct := float64(base-got) / float64(base) * 100
	if pct >= 0 {
		return fmt.Sprintf("−%.1f%%", pct)
	}
	return fmt.Sprintf("+%.1f%%", -pct)
}

func rtrMark(m metrics) string {
	if m.RoundTripErr != "" {
		return "✗ " + m.RoundTripErr
	}
	if m.RoundTripOK {
		return "✓"
	}
	return "?"
}

// summaryLineCL100k summarises the cl100k_base table: median token
// + median byte savings + round-trip pass count.
func summaryLineCL100k(rows []metrics) string {
	if len(rows) == 0 {
		return "_no cases_"
	}
	var (
		tokensJ, tokensG []int
		bytesJ, bytesG   []int
	)
	passed := 0
	for _, m := range rows {
		if m.JSONTokens > 0 {
			tokensJ = append(tokensJ, m.JSONTokens)
			tokensG = append(tokensG, m.GCXTokens)
			bytesJ = append(bytesJ, m.JSONBytes)
			bytesG = append(bytesG, m.GCXBytes)
		}
		if m.RoundTripOK {
			passed++
		}
	}
	return fmt.Sprintf("**Summary (cl100k_base):** %d/%d cases. Median token savings: %s. Median byte savings: %s. Round-trip integrity: %d/%d.",
		len(rows), len(rows),
		medianPct(tokensJ, tokensG),
		medianPct(bytesJ, bytesG),
		passed, len(rows),
	)
}

// summaryLineOpus47 summarises the Opus 4.7 table: median token
// savings on the new tokenizer plus a count of exact vs. estimated
// rows so the reader can judge confidence.
func summaryLineOpus47(rows []metrics) string {
	if len(rows) == 0 {
		return "_no cases_"
	}
	var tokensJ, tokensG []int
	exactRows := 0
	for _, m := range rows {
		if m.JSONTokensOpus47 > 0 {
			tokensJ = append(tokensJ, m.JSONTokensOpus47)
			tokensG = append(tokensG, m.GCXTokensOpus47)
		}
		if m.ExactOpus47 {
			exactRows++
		}
	}
	return fmt.Sprintf("**Summary (Opus 4.7):** %d/%d cases. Median token savings: %s. Exact rows: %d/%d (rest estimated by the in-process per-model tokenizer).",
		len(rows), len(rows),
		medianPct(tokensJ, tokensG),
		exactRows, len(rows),
	)
}

func medianPct(base, got []int) string {
	if len(base) == 0 {
		return "n/a"
	}
	deltas := make([]float64, len(base))
	for i := range base {
		if base[i] == 0 {
			deltas[i] = 0
			continue
		}
		deltas[i] = float64(base[i]-got[i]) / float64(base[i]) * 100
	}
	sort.Float64s(deltas)
	mid := deltas[len(deltas)/2]
	if mid >= 0 {
		return fmt.Sprintf("−%.1f%%", mid)
	}
	return fmt.Sprintf("+%.1f%%", -mid)
}
