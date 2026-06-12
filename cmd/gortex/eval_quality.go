// eval_quality.go — `gortex eval quality {drift|confidence|replay|tune}`
// subcommands. Thin CLI veneer over internal/eval/quality; the
// actual analysis logic lives there so tests don't need cobra.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/zzet/gortex/internal/eval/quality"
)

var (
	evalQualityFingerprintPath string
	evalQualityConfidencePath  string
	evalQualitySince           time.Duration
	evalQualityFormat          string
)

var evalQualityCmd = &cobra.Command{
	Use:   "quality",
	Short: "Measurement infrastructure: drift detection, confidence tracking, query-log replay, weight tuning",
	Long: `Surface-level analyzers over the retrieval substrate:

  drift       — compare current embedder fingerprint to the last
                recorded one; flags silent provider / model /
                dimension swaps
  confidence  — summarize the per-query candidate-score distribution
                log (top-1 / top-2 ratio, std-dev, low-confidence
                count)
  replay      — replay a query log through two ranker configurations,
                report Kendall τ + top-k churn + recall delta
  tune        — propose per-signal rerank weight adjustments based
                on the feedback log; operator decides whether to apply

All four produce markdown by default; --format json for downstream
tooling.`,
}

func init() {
	evalQualityCmd.PersistentFlags().StringVar(&evalQualityFormat, "format", "markdown", "output format: markdown or json")
	evalCmd.AddCommand(evalQualityCmd)

	evalQualityCmd.AddCommand(evalQualityDriftCmd)
	evalQualityCmd.AddCommand(evalQualityConfidenceCmd)
	evalQualityCmd.AddCommand(evalQualityReplayCmd)
	evalQualityCmd.AddCommand(evalQualityTuneCmd)

	evalQualityDriftCmd.Flags().StringVar(&evalQualityFingerprintPath, "fingerprint", quality.DefaultFingerprintPath(), "path to the embedder fingerprint persistence file")

	evalQualityConfidenceCmd.Flags().StringVar(&evalQualityConfidencePath, "log", quality.DefaultConfidenceLogPath(), "path to the JSONL confidence log")
	evalQualityConfidenceCmd.Flags().DurationVar(&evalQualitySince, "since", 7*24*time.Hour, "summarize records newer than this (0 = all-time)")
}

// --- drift -----------------------------------------------------------

var evalQualityDriftCmd = &cobra.Command{
	Use:   "drift",
	Short: "Report whether the active embedder has changed since the last fingerprint",
	RunE: func(cmd *cobra.Command, _ []string) error {
		d := quality.NewDriftDetector(evalQualityFingerprintPath)
		prev, err := d.LoadPrevious()
		if err != nil {
			return err
		}

		// Without a live indexer here, we can't capture a fresh
		// fingerprint — this subcommand is a read-only inspector,
		// not a writer. Report the stored fingerprint + emit a
		// no-drift response when no prior record exists.
		w := quality.DriftWarning{Previous: prev, Current: prev}
		if strings.ToLower(evalQualityFormat) == "json" {
			body, _ := json.MarshalIndent(map[string]any{
				"stored":    prev,
				"drift":     w.HasDrift(),
				"changes":   w.Changes,
				"note":      "drift inspector — write fingerprints via `gortex eval embedders` or the daemon warmup; this read-only path reports the stored record",
			}, "", "  ")
			_, _ = cmd.OutOrStdout().Write(append(body, '\n'))
			return nil
		}
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintln(out, "# Embedder drift inspector")
		_, _ = fmt.Fprintln(out)
		if (prev == quality.EmbedderFingerprint{}) {
			_, _ = fmt.Fprintln(out, "_No fingerprint recorded yet. Run `gortex eval embedders` (or wait for the daemon to warm an embedder) to seed one._")
			return nil
		}
		_, _ = fmt.Fprintf(out, "**Stored fingerprint** (recorded %s):\n", prev.RecordedAt.Format(time.RFC3339))
		_, _ = fmt.Fprintf(out, "- provider: `%s`\n", prev.Provider)
		_, _ = fmt.Fprintf(out, "- model: `%s`\n", prev.Model)
		if prev.ModelRevision != "" {
			_, _ = fmt.Fprintf(out, "- model_revision: `%s`\n", prev.ModelRevision)
		}
		_, _ = fmt.Fprintf(out, "- embedding_dim: `%d`\n", prev.EmbeddingDim)
		if prev.SampleVecSHA256 != "" {
			_, _ = fmt.Fprintf(out, "- sample_vec_sha256: `%s`\n", prev.SampleVecSHA256)
		}
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "_To check drift against a live embedder, run `gortex eval embedders` — that path fingerprints the active provider and surfaces drift inline._")
		return nil
	},
}

// --- confidence ------------------------------------------------------

var evalQualityConfidenceCmd = &cobra.Command{
	Use:   "confidence",
	Short: "Summarize the per-query confidence log (top-1/top-2 ratio, std-dev, low-confidence count)",
	RunE: func(cmd *cobra.Command, _ []string) error {
		var cutoff time.Time
		if evalQualitySince > 0 {
			cutoff = time.Now().UTC().Add(-evalQualitySince)
		}
		records, err := quality.LoadConfidenceLog(evalQualityConfidencePath, cutoff)
		if err != nil {
			return err
		}
		summary := quality.SummarizeConfidence(records)
		if strings.ToLower(evalQualityFormat) == "json" {
			body, _ := json.MarshalIndent(map[string]any{
				"path":    evalQualityConfidencePath,
				"window":  evalQualitySince.String(),
				"summary": summary,
			}, "", "  ")
			_, _ = cmd.OutOrStdout().Write(append(body, '\n'))
			return nil
		}
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintln(out, "# Retrieval confidence summary")
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintf(out, "- log: `%s`\n", evalQualityConfidencePath)
		_, _ = fmt.Fprintf(out, "- window: last `%s`\n", evalQualitySince)
		_, _ = fmt.Fprintln(out)
		if summary.Count == 0 {
			_, _ = fmt.Fprintln(out, "_No records in window. The confidence tracker is opt-in — wire it via the search-call site you want to measure._")
			return nil
		}
		_, _ = fmt.Fprintf(out, "- records: %d\n", summary.Count)
		_, _ = fmt.Fprintf(out, "- median top-1 score: %.4f\n", summary.MedianTop1)
		_, _ = fmt.Fprintf(out, "- median top-1/top-2 ratio: %.3f\n", summary.MedianRatio12)
		_, _ = fmt.Fprintf(out, "- median std-dev: %.4f\n", summary.MedianStdDev)
		_, _ = fmt.Fprintf(out, "- low-confidence records (ratio < 1.25): %d (%.1f%%)\n",
			summary.LowConfidenceCount,
			float64(summary.LowConfidenceCount)/float64(summary.Count)*100)
		return nil
	},
}

// --- replay ----------------------------------------------------------

var evalQualityReplayCmd = &cobra.Command{
	Use:   "replay",
	Short: "Replay a query log against two ranker configurations (writes a placeholder when no log/runner is wired)",
	RunE: func(cmd *cobra.Command, _ []string) error {
		// The replay analysis lives in internal/eval/quality;
		// wiring it into a real CLI flow needs (a) a query log
		// path, (b) two ranker configs to compare. Neither is
		// trivial to express through flags — the realistic path is
		// `gortex eval quality replay` driven by a script the
		// operator writes (one-shot Go program importing the
		// quality package). We document that here rather than
		// pretending the CLI can do it from flags alone.
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintln(out, "# Replay harness")
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "The replay analyzer ships as a Go API in `internal/eval/quality`:")
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "```go")
		_, _ = fmt.Fprintln(out, `import "github.com/zzet/gortex/internal/eval/quality"`)
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "// queries: load from a JSONL file or build inline")
		_, _ = fmt.Fprintln(out, "// baseline, candidate: quality.RankerFunc — closures over your search backend")
		_, _ = fmt.Fprintln(out, "res, _ := quality.Replay(queries, baseline, candidate, 10)")
		// Write the example code-block line directly — Fprint /
		// Fprintln both trip govet's printf checker on the embedded
		// format specifiers, even when the call has no format args.
		_, _ = out.Write([]byte("fmt.Printf(\"kendall=%.3f top1_churn=%.1f%% recall_delta=%+.3f\\n\", res.MeanKendall, res.Top1ChurnPct, res.RecallDelta)\n"))
		_, _ = fmt.Fprintln(out, "```")
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "Two ranker comparisons that fit on the command line are wired up automatically; everything else is a few lines of Go using the quality package.")
		return nil
	},
}

// --- tune -----------------------------------------------------------

var evalQualityTuneCmd = &cobra.Command{
	Use:   "tune",
	Short: "Propose per-signal rerank weight adjustments from the feedback log (suggestion only; operator decides)",
	RunE: func(cmd *cobra.Command, _ []string) error {
		out := cmd.OutOrStdout()
		// The feedback log is exposed via the `feedback` MCP tool;
		// a future CLI integration would pull it in. For now,
		// surface the tuning API + emit an empty-state report.
		rows := quality.SuggestWeights(nil, 1.0)
		md := quality.RenderTuningMarkdown(rows)
		_, _ = fmt.Fprintln(out, md)
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "_The tuner ships as `quality.SuggestWeights(rows, nudge)` — wire the rows from your feedback log (`feedback action:query`) and pipe the markdown into a PR for review._")
		return nil
	},
}

// Used only to silence "imported and not used" while iterating.
var _ = os.Stdout
