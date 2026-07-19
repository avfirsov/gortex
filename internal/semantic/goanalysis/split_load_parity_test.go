package goanalysis

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_sqlite"
)

// The export-data load (default) and the full-closure source load
// (GORTEX_GOTYPES_NEEDDEPS=1) must produce the same graph delta — same
// confirmations, same additions, same external/module classification. The
// fixture exercises the risky seam: a stdlib call, which the closure mode
// classifies from the walked dependency package and the split mode from the
// metadata index.
func TestSplitLoadParityWithClosureLoad(t *testing.T) {
	run := func(t *testing.T, needDeps string) (int, int, int, []string) {
		t.Setenv("GORTEX_GOTYPES_NEEDDEPS", needDeps)
		root := resolvedTempDir(t)
		writeGoMod(t, root, "example.com/parity")
		writeFile(t, root, "main.go", `package parity

import "fmt"

func Greet() {
	fmt.Println(Message())
}

func Message() string { return "hi" }
`)

		store, err := store_sqlite.Open(filepath.Join(t.TempDir(), "graph.sqlite"))
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, store.Close()) })

		store.AddBatch([]*graph.Node{
			{ID: "main.go::Greet", Kind: graph.KindFunction, Name: "Greet", FilePath: "main.go", StartLine: 5, EndLine: 7, Language: "go"},
			{ID: "main.go::Message", Kind: graph.KindFunction, Name: "Message", FilePath: "main.go", StartLine: 9, EndLine: 9, Language: "go"},
			{ID: "main.go", Kind: graph.KindFile, Name: "main.go", FilePath: "main.go", Language: "go"},
		}, []*graph.Edge{{
			From: "main.go::Greet", To: "unresolved::Message", Kind: graph.EdgeCalls,
			FilePath: "main.go", Line: 6, Confidence: 0.25, ConfidenceLabel: "HEURISTIC",
		}})

		provider := newTestProvider(t)
		t.Cleanup(func() { require.NoError(t, provider.Close()) })

		res, err := provider.EnrichRepo(store, "", root)
		require.NoError(t, err)
		require.False(t, res.Degraded, "a healthy root-manifest module must fully enrich")

		var moduleNodes []string
		for _, n := range store.AllNodes() {
			if n.Kind == graph.KindModule {
				moduleNodes = append(moduleNodes, n.ID)
			}
		}
		return res.EdgesConfirmed, res.EdgesAdded, res.NodesEnriched, moduleNodes
	}

	var closureConfirmed, closureAdded, closureNodes int
	var closureModules []string
	t.Run("closure", func(t *testing.T) {
		closureConfirmed, closureAdded, closureNodes, closureModules = run(t, "1")
	})
	var splitConfirmed, splitAdded, splitNodes int
	var splitModules []string
	t.Run("split", func(t *testing.T) {
		splitConfirmed, splitAdded, splitNodes, splitModules = run(t, "")
	})

	require.Equal(t, closureConfirmed, splitConfirmed, "confirmed edges must match across load modes")
	require.Equal(t, closureAdded, splitAdded, "added edges must match across load modes")
	require.Equal(t, closureNodes, splitNodes, "enriched nodes must match across load modes")
	require.ElementsMatch(t, closureModules, splitModules, "module classification must match across load modes")
	require.Contains(t, splitModules, stdlibModuleID, "the stdlib call must classify to the stdlib module node")
}
