package main

// PURPOSE — daemonless `gortex analyze` cobra command: indexes a repository
// path entirely in-process (no daemon socket) and runs one of the supported
// analyzer kinds against the resulting graph, printing either JSON or a
// human-readable text summary.
// RATIONALE — gives CI pipelines and one-shot scripts access to graph
// analytics without requiring a running daemon; the full indexing pipeline
// runs in the calling process and exits when done.
// KEYWORDS — analyze, daemonless, synthesizers, resolution_outcomes, CLI

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/analyzer"
	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/indexer"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
)

var (
	analyzeKind   string
	analyzePath   string
	analyzeFormat string
)

// supportedAnalyzeKinds lists the analyzer kinds accepted by the --kind flag.
var supportedAnalyzeKinds = []string{
	"synthesizers",
	"resolution_outcomes",
}

var analyzeCmd = &cobra.Command{
	Use:   "analyze",
	Short: "Index a repository in-process and run an analyzer (no daemon required)",
	Long: `Indexes the repository at --path entirely in-process — no daemon, no socket —
then runs the specified --kind analyzer and prints results.

Supported kinds:
  synthesizers        — Synthesized edge groups by framework-dispatch pass
  resolution_outcomes — Taxonomy of unresolved call/reference edges`,
	RunE: runAnalyze,
}

func init() {
	analyzeCmd.Flags().StringVar(&analyzeKind, "kind", "", "analyzer kind: synthesizers|resolution_outcomes (required)")
	analyzeCmd.Flags().StringVar(&analyzePath, "path", ".", "repository path to index")
	analyzeCmd.Flags().StringVar(&analyzeFormat, "format", "text", "output format: json|text")
	_ = analyzeCmd.MarkFlagRequired("kind")
	rootCmd.AddCommand(analyzeCmd)
}

// runAnalyze is the RunE for analyzeCmd. It loads config, builds the graph +
// registry + parser in-process, indexes the target path, then dispatches to
// the requested analyzer kind.
func runAnalyze(cmd *cobra.Command, _ []string) error {
	// Validate --kind early so users get a clear error before any indexing work.
	if !isSupportedKind(analyzeKind) {
		return fmt.Errorf("unsupported --kind %q; supported kinds: synthesizers, resolution_outcomes", analyzeKind)
	}

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Mirror the index.go pattern: default Workers to NumCPU when config
	// leaves it at zero.
	if cfg.Index.Workers == 0 {
		cfg.Index.Workers = runtime.NumCPU()
	}

	// Build the in-process graph + indexer (no daemon involved). Mirrors the
	// registry construction in `gortex index`.
	logger := zap.NewNop()
	g := graph.New()
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	languages.RegisterCustomGrammars(reg, cfg.Index.Grammars, logger)
	languages.RegisterExtractorPlugins(reg, cfg.Index.ExtractorPlugins, logger)
	languages.RegisterFallbackChunkers(reg, cfg.Index.FallbackChunkers, logger)
	idx := indexer.New(g, reg, cfg.Index, logger)

	ctx := context.Background()
	if _, err := idx.IndexCtx(ctx, analyzePath); err != nil {
		return fmt.Errorf("indexing %s: %w", analyzePath, err)
	}

	// Dispatch to the requested analyzer kind.
	switch analyzeKind {
	case "synthesizers":
		return runSynthesizers(cmd, g)
	case "resolution_outcomes":
		return runResolutionOutcomes(cmd, g)
	default:
		// Unreachable — validated above, but keeps the compiler happy.
		return fmt.Errorf("unsupported kind: %s", analyzeKind)
	}
}

// isSupportedKind returns true if kind is in supportedAnalyzeKinds.
func isSupportedKind(kind string) bool {
	for _, k := range supportedAnalyzeKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// runSynthesizers analyzes synthesized edge groups and prints them.
func runSynthesizers(cmd *cobra.Command, g graph.Store) error {
	result := analyzer.AnalyzeSynthesizers(g)

	switch analyzeFormat {
	case "json":
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	default:
		fmt.Fprintf(cmd.OutOrStdout(), "synthesizers: groups=%d total_edges=%d\n",
			len(result.Synthesizers), result.TotalEdges)
		for _, row := range result.Synthesizers {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s: edges=%d\n", row.Name, row.Edges)
		}
		return nil
	}
}

// runResolutionOutcomes analyzes unresolved edge taxonomy and prints it.
func runResolutionOutcomes(cmd *cobra.Command, g graph.Store) error {
	result := analyzer.AnalyzeResolutionOutcomes(g, "", 50)

	switch analyzeFormat {
	case "json":
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	default:
		fmt.Fprintf(cmd.OutOrStdout(), "resolution_outcomes: total=%d\n", result.Total)
		for reason, count := range result.ByReason {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s: %d\n", reason, count)
		}
		return nil
	}
}
