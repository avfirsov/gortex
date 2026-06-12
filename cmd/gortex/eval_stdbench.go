package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zzet/gortex/internal/eval/stdbench"
	"github.com/zzet/gortex/internal/search"
)

var (
	evalStdbenchBench   string
	evalStdbenchDataset string
	evalStdbenchFormat  string
	evalStdbenchOut     string
)

var evalStdbenchCmd = &cobra.Command{
	Use:   "stdbench",
	Short: "Run a standardized retrieval benchmark (CoIR / SWE-ContextBench / ContextBench)",
	Long: `Runs Gortex's BM25 text retrieval against a standardized code-retrieval
benchmark and reports Recall@K, Precision@K, NDCG@10, and MRR.

Benchmarks (--bench):
  coir              CoIR (Code Information Retrieval, ACL 2025). --dataset is
                    a BEIR-layout directory: corpus.jsonl + queries.jsonl +
                    qrels/<split>.tsv.
  swe-contextbench  SWE-ContextBench (arXiv 2602.08316). --dataset is a JSONL
                    file, one context-retrieval task per line.
  contextbench      ContextBench (arXiv 2602.05892). Same JSONL task layout.

The JSONL task line schema is {id, query, relevant:[ids], candidates:[{id,
text}]}; per-task candidate pools are merged into one corpus. Field-name
aliases (question / problem_statement, gold / context, documents / pool)
are accepted.

Typical use:

  gortex eval stdbench --bench coir --dataset datasets/coir/codesearchnet
  gortex eval stdbench --bench contextbench --dataset datasets/contextbench.jsonl --format json`,
	RunE: runEvalStdbench,
}

func init() {
	evalStdbenchCmd.Flags().StringVar(&evalStdbenchBench, "bench", "", "benchmark: coir | swe-contextbench | contextbench")
	evalStdbenchCmd.Flags().StringVar(&evalStdbenchDataset, "dataset", "", "dataset path: a BEIR directory for coir, a .jsonl file for the others")
	evalStdbenchCmd.Flags().StringVar(&evalStdbenchFormat, "format", "markdown", "output format: markdown or json")
	evalStdbenchCmd.Flags().StringVar(&evalStdbenchOut, "out", "", "output file (default: stdout)")
	evalCmd.AddCommand(evalStdbenchCmd)
}

func runEvalStdbench(_ *cobra.Command, _ []string) error {
	if evalStdbenchDataset == "" {
		return fmt.Errorf("--dataset is required")
	}

	var (
		ds  stdbench.Dataset
		err error
	)
	switch strings.ToLower(strings.TrimSpace(evalStdbenchBench)) {
	case "coir":
		ds, err = stdbench.LoadCoIR(evalStdbenchDataset)
	case "swe-contextbench", "swe_contextbench", "swecontextbench":
		ds, err = stdbench.LoadSWEContextBench(evalStdbenchDataset)
	case "contextbench":
		ds, err = stdbench.LoadContextBench(evalStdbenchDataset)
	default:
		return fmt.Errorf("unknown --bench %q (want coir, swe-contextbench, or contextbench)", evalStdbenchBench)
	}
	if err != nil {
		return fmt.Errorf("loading %s: %w", evalStdbenchBench, err)
	}

	if len(ds.Corpus) == 0 {
		return fmt.Errorf("benchmark %q has an empty corpus — CoIR ships corpus.jsonl; "+
			"for the JSONL task benchmarks each task must carry a `candidates` pool", evalStdbenchBench)
	}
	if ds.RelevantCount() == 0 {
		return fmt.Errorf("benchmark %q carries no relevance judgements to score against", evalStdbenchBench)
	}

	// Index the corpus into Gortex's BM25 backend — the same text
	// retrieval search_symbols runs — and rank doc IDs per query.
	bm := search.NewBM25()
	for _, d := range ds.Corpus {
		bm.Add(d.ID, d.Text)
	}
	retrieve := func(query string, k int) []string {
		hits := bm.Search(query, k)
		ids := make([]string, 0, len(hits))
		for _, h := range hits {
			ids = append(ids, h.ID)
		}
		return ids
	}

	metrics := stdbench.Evaluate(ds, retrieve, nil)

	var rendered string
	if strings.EqualFold(evalStdbenchFormat, "json") {
		b, err := json.MarshalIndent(metrics, "", "  ")
		if err != nil {
			return err
		}
		rendered = string(b)
	} else {
		rendered = metrics.Markdown()
	}

	if evalStdbenchOut != "" {
		if err := os.WriteFile(evalStdbenchOut, []byte(rendered+"\n"), 0o644); err != nil {
			return err
		}
		fmt.Printf("wrote %s\n", evalStdbenchOut)
		return nil
	}
	fmt.Println(rendered)
	return nil
}
