package main

// export_understand.go wires `gortex export understand` — the Action layer for
// the Understand-Anything exporter. It indexes the target repository
// in-process (the same path `gortex index` uses), then renders the graph to
// `.understand-anything/knowledge-graph.json` via
// exporter.WriteUnderstandAnything.
//
// WHY in-process rather than via the daemon: the L1 slice is bounded to
// CLI + library + tests; the MCP `export_graph` tool path is a later slice. A
// self-contained index-then-write keeps the subcommand usable without a
// running daemon and keeps the change off the MCP surface.
//
// WHY the timestamp and commit are resolved HERE: business_requirements §12
// forbids time.Now() / git inside the pure exporter core. This Action layer is
// the sole supplier of AnalyzedAt (RFC3339) and GitCommit; it passes them down
// as plain strings so the core stays a pure, deterministic Calculation.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/exporter"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/indexer"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
)

var (
	uaExportOut         string
	uaExportGranularity string
	uaExportGeneric     bool
	uaExportProjectName string
	uaExportRepo        string
	uaExportPretty      bool
)

// uaDefaultOutPath is the conventional UA output location relative to the
// indexed repo root.
const uaDefaultOutPath = ".understand-anything/knowledge-graph.json"

var exportUnderstandCmd = &cobra.Command{
	Use:   "understand [path]",
	Short: "Export the graph to Understand-Anything (understand-anything@1) or generic@1",
	Long: `Index the repository in-process and render its code graph into the
Understand-Anything knowledge-graph format (understand-anything@1), or the
reduced generic@1 {nodes, edges} projection with --generic.

The output is a deterministic projection: gortex node/edge kinds map to the UA
schema's closed enums, gortex-specific fields ride along as passthrough keys,
and nothing is dropped silently — every non-emitted node/edge is counted and
logged with a reason. Load the result into the Understand-Anything dashboard or
publish it to the understand-quickly registry.

  gortex export understand                       # writes .understand-anything/knowledge-graph.json
  gortex export understand /path/to/repo --pretty
  gortex export understand --granularity full    # keep params/locals/dataflow as concepts
  gortex export understand --generic --out kg.json
`,
	Args: cobra.MaximumNArgs(1),
	RunE: runExportUnderstand,
}

func init() {
	exportUnderstandCmd.Flags().StringVar(&uaExportOut, "out", "",
		"output file (default: <repo>/.understand-anything/knowledge-graph.json)")
	exportUnderstandCmd.Flags().StringVar(&uaExportGranularity, "granularity", exporter.GranularitySlim,
		"slim (default; drops params/locals/builtins/dataflow) | full (keeps them as concepts)")
	exportUnderstandCmd.Flags().BoolVar(&uaExportGeneric, "generic", false,
		"emit the reduced generic@1 {nodes, edges} projection instead of understand-anything@1")
	exportUnderstandCmd.Flags().StringVar(&uaExportProjectName, "project-name", "",
		"project name in the UA envelope (default: repo basename)")
	exportUnderstandCmd.Flags().StringVar(&uaExportRepo, "repo", "",
		"filter to one repo prefix (default: all)")
	exportUnderstandCmd.Flags().BoolVar(&uaExportPretty, "pretty", false,
		"indent the JSON output for human reading")
	exportCmd.AddCommand(exportUnderstandCmd)
}

// runExportUnderstand indexes the target path, resolves the Action-supplied
// metadata (analyzedAt, gitCommit, project name, out path), writes the UA
// graph, and logs the input→output accounting.
func runExportUnderstand(cmd *cobra.Command, args []string) error {
	logger := newLogger()
	defer func() { _ = logger.Sync() }()

	repoPath := "."
	if len(args) == 1 {
		repoPath = args[0]
	}
	absRepo, err := filepath.Abs(repoPath)
	if err != nil {
		return fmt.Errorf("resolve repo path: %w", err)
	}

	g, err := indexRepoInProcess(absRepo, logger)
	if err != nil {
		return err
	}

	// Resolve the out path; default under the repo root, create the dir.
	outPath := uaExportOut
	if outPath == "" {
		outPath = filepath.Join(absRepo, uaDefaultOutPath)
	}
	absOut, err := filepath.Abs(outPath)
	if err != nil {
		return fmt.Errorf("resolve output path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absOut), 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	projectName := uaExportProjectName
	if projectName == "" {
		projectName = filepath.Base(absRepo)
	}

	opts := exporter.UAOptions{
		Options:     exporter.Options{Repo: uaExportRepo, Pretty: uaExportPretty},
		Granularity: uaExportGranularity,
		Generic:     uaExportGeneric,
		ProjectName: projectName,
		AnalyzedAt:  time.Now().UTC().Format(time.RFC3339),
		GitCommit:   gitCommitHash(absRepo),
	}

	// Snapshot the accounting BEFORE writing so the log reflects the same
	// numbers the file carries. ToUnderstandAnything is pure, so calling it
	// here and inside WriteUnderstandAnything yields identical results.
	_, dropped := exporter.ToUnderstandAnything(g, opts)
	droppedNodes, droppedEdges := countDropped(dropped)

	start := time.Now()
	f, err := os.Create(absOut)
	if err != nil {
		return fmt.Errorf("create %q: %w", absOut, err)
	}
	stats, werr := exporter.WriteUnderstandAnything(f, g, opts)
	closeErr := f.Close()
	if werr != nil {
		return fmt.Errorf("write understand-anything: %w", werr)
	}
	if closeErr != nil {
		return fmt.Errorf("close %q: %w", absOut, closeErr)
	}

	logger.Info("export understand-anything",
		zap.String("repo", absRepo),
		zap.String("granularity", opts.Granularity),
		zap.Bool("generic", opts.Generic),
		zap.Int("nodes_in", stats.NodesWritten+droppedNodes),
		zap.Int("nodes_out", stats.NodesWritten),
		zap.Int("edges_in", stats.EdgesWritten+droppedEdges),
		zap.Int("edges_out", stats.EdgesWritten),
		zap.Int("dropped_nodes", droppedNodes),
		zap.Int("dropped_edges", droppedEdges),
		zap.String("out_path", absOut),
		zap.Int64("bytes", stats.BytesWritten),
		zap.Duration("duration", time.Since(start)),
	)

	_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
		"[gortex export understand] wrote %d nodes, %d edges (%d dropped nodes, %d dropped edges) to %s\n",
		stats.NodesWritten, stats.EdgesWritten, droppedNodes, droppedEdges, absOut)
	return nil
}

// indexRepoInProcess builds an in-memory graph by indexing the repo at root,
// mirroring the `gortex index` path. Returns the populated store.
func indexRepoInProcess(root string, logger *zap.Logger) (graph.Store, error) {
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return nil, err
	}
	g := graph.New()
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	languages.RegisterCustomGrammars(reg, cfg.Index.Grammars, logger)
	languages.RegisterExtractorPlugins(reg, cfg.Index.ExtractorPlugins, logger)
	languages.RegisterFallbackChunkers(reg, cfg.Index.FallbackChunkers, logger)
	idx := indexer.New(g, reg, cfg.Index, logger)
	if _, err := idx.IndexCtx(context.Background(), root); err != nil {
		return nil, fmt.Errorf("indexing %s: %w", root, err)
	}
	return g, nil
}

// countDropped tallies the []Dropped audit trail into (nodes, edges) using the
// same "->"-join convention buildUAGraph uses for edge drop records.
func countDropped(dropped []exporter.Dropped) (nodes, edges int) {
	for _, d := range dropped {
		isEdge := false
		for i := 0; i+1 < len(d.ID); i++ {
			if d.ID[i] == '-' && d.ID[i+1] == '>' {
				isEdge = true
				break
			}
		}
		if isEdge {
			edges++
		} else {
			nodes++
		}
	}
	return nodes, edges
}
