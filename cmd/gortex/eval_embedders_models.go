// eval_embedders_models.go — registry of next-generation embedder
// model specs that `gortex eval embedders` can probe / benchmark
// alongside the existing MiniLM-L6-v2 ONNX variants. Each spec
// carries an install hint and a loader that returns an actionable
// "not installed" error when its external dependency isn't local —
// so the harness never tries to silently download GB-scale models.
//
// The split: MiniLM-L6-v2 variants live in the existing flow
// (eval_embedders.go); next-gen specs live here. Listing via
// `gortex eval embedders --list` shows both surfaces so an operator
// can pick what to benchmark without guessing.
package main

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// modelSpec describes one embedder candidate the registry knows
// about. Loader returns a working provider on success, or an error
// when the model's external dependency is missing — the bench skips
// that row cleanly rather than aborting.
type modelSpec struct {
	Name        string
	Provider    string // friendly provider grouping (Google / Alibaba / NVIDIA / Model2Vec / …)
	Dim         int    // embedding dimensionality (target; loader may report different)
	InstallHint string
	// LoaderCheck cheaply verifies whether the external dependency
	// looks installable / loadable. Returns nil on "looks good" and
	// the install hint on "missing". Cheap by design so `--list`
	// can run it for every spec without paying for an actual load.
	LoaderCheck func() error
}

// nextGenModelSpecs is the registry of model specs `gortex eval
// embedders` knows about beyond MiniLM-L6-v2. Adding a new model =
// one entry here + the install hint in the docs. Order is stable so
// the --list output is deterministic.
func nextGenModelSpecs() []modelSpec {
	return []modelSpec{
		{
			Name:        "embedding-gemma",
			Provider:    "Google",
			Dim:         768,
			InstallHint: "pip install sentence-transformers transformers torch; model: google/embeddinggemma-300m (~1.2 GB first-run download)",
			LoaderCheck: pythonModulePresent("sentence_transformers"),
		},
		{
			Name:        "qwen3-embedding-8b",
			Provider:    "Alibaba",
			Dim:         4096,
			InstallHint: "pip install sentence-transformers transformers torch; model: Qwen/Qwen3-Embedding-8B (~16 GB first-run download; requires a 24 GB+ GPU for inference)",
			LoaderCheck: pythonModulePresent("sentence_transformers"),
		},
		{
			Name:        "nv-embed-v2",
			Provider:    "NVIDIA",
			Dim:         4096,
			InstallHint: "pip install sentence-transformers transformers torch; model: nvidia/NV-Embed-v2 (~15 GB first-run download; gated — accept the licence at huggingface.co/nvidia/NV-Embed-v2 before downloading)",
			LoaderCheck: pythonModulePresent("sentence_transformers"),
		},
		{
			Name:        "potion-code-16m",
			Provider:    "Model2Vec",
			Dim:         512,
			InstallHint: "pip install model2vec; model: minishlab/potion-code-16M (~32 MB first-run download; pure-CPU, no GPU needed)",
			LoaderCheck: pythonModulePresent("model2vec"),
		},
	}
}

// modelSpecByName looks up a spec case-insensitively. Returns nil
// when no spec matches — caller decides whether to error or skip.
func modelSpecByName(name string) *modelSpec {
	for _, s := range nextGenModelSpecs() {
		if strings.EqualFold(s.Name, name) {
			return &s
		}
	}
	return nil
}

// pythonModulePresent returns a LoaderCheck that runs
// `python3 -c "import <module>"`. Fast (sub-100ms) and honest about
// what's installed.
func pythonModulePresent(module string) func() error {
	return func() error {
		pythons := []string{"python3", "python"}
		var lastErr error
		for _, py := range pythons {
			if _, err := exec.LookPath(py); err != nil {
				continue
			}
			cmd := exec.Command(py, "-c", "import "+module)
			if err := cmd.Run(); err == nil {
				return nil
			} else {
				lastErr = err
			}
		}
		if lastErr == nil {
			return fmt.Errorf("python3 not on PATH")
		}
		return fmt.Errorf("python module %q not importable", module)
	}
}

// --- list subcommand -----------------------------------------------

// evalEmbeddersListCmd surfaces the model registry without running
// any benchmark — useful in CI to confirm wiring and for users to
// pick a model to actually benchmark.
var evalEmbeddersListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered embedder model specs (next-gen + MiniLM variants) with availability",
	RunE: func(cmd *cobra.Command, _ []string) error {
		w := cmd.OutOrStdout()
		_, _ = fmt.Fprintln(w, "# Embedder model registry")
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintln(w, "## Next-gen specs (Python-backed)")
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintln(w, "| name | provider | dim | available | install hint |")
		_, _ = fmt.Fprintln(w, "|------|----------|----:|:---------:|--------------|")
		specs := nextGenModelSpecs()
		// Stable display order: by provider then name.
		sort.Slice(specs, func(i, j int) bool {
			if specs[i].Provider != specs[j].Provider {
				return specs[i].Provider < specs[j].Provider
			}
			return specs[i].Name < specs[j].Name
		})
		for _, s := range specs {
			avail := "✓"
			if s.LoaderCheck != nil {
				if err := s.LoaderCheck(); err != nil {
					avail = "✗"
				}
			}
			_, _ = fmt.Fprintf(w, "| %s | %s | %d | %s | %s |\n",
				s.Name, s.Provider, s.Dim, avail, s.InstallHint)
		}
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintln(w, "## MiniLM-L6-v2 ONNX variants (in-process via Hugot)")
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintln(w, "All variants ship in-process — no external install required. Pass to `gortex eval embedders --variants <name>`.")
		_, _ = fmt.Fprintln(w)
		for _, name := range []string{"fp32", "o2", "o3", "o4", "qint8_arm64", "qint8_avx512", "quint8_avx2"} {
			_, _ = fmt.Fprintf(w, "- %s\n", name)
		}
		return nil
	},
}

func init() {
	evalEmbeddersCmd.AddCommand(evalEmbeddersListCmd)
}
