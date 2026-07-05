package lsp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_sqlite"
	"github.com/zzet/gortex/internal/semantic"
)

// TestLSP_Provider_SkipsAlreadyStampedNodes_LightScan mirrors
// TestLSP_Provider_SkipsAlreadyStampedNodes but backs the graph with a real
// sqlite Store, which implements graph.LightNodeReader — exercising the
// repoScopedNodesLight branch the in-memory-graph test never reaches. The
// critical extra assertion: a non-promoted meta key present on the fresh
// node before enrichment must survive the light-scan → candidate refetch
// (GetNodesByIDs) → hover-stamp → AddBatch round trip. Losing it would mean
// the light scan's stripped-down Meta got persisted back in place of the
// full one — silently discarding whatever else the graph had recorded for
// that node.
func TestLSP_Provider_SkipsAlreadyStampedNodes_LightScan(t *testing.T) {
	t.Setenv("GORTEX_LSP_SWEEP", "full")
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(repoRoot, "main.go"),
		[]byte("package main\n\nfunc F() string { return \"hi\" }\n\nfunc G() int { return 0 }\n"),
		0o644,
	))

	var hoverCalls atomic.Int64
	server := newFakeLSPServer()
	server.handle("textDocument/hover", func(params json.RawMessage) (any, *jsonRPCError) {
		hoverCalls.Add(1)
		return map[string]any{
			"contents": map[string]any{"kind": "plaintext", "value": "func G() int"},
		}, nil
	})

	p, cleanup := providerWithFakeServer(t, server, []string{"go"})
	defer cleanup()

	g, err := store_sqlite.Open(filepath.Join(t.TempDir(), "enrich.sqlite"))
	require.NoError(t, err)
	defer g.Close()
	var _ graph.LightNodeReader = g // the branch this test targets

	// F is already stamped by a prior pass — must not be re-hovered.
	g.AddNode(&graph.Node{
		ID: "main.go::F", Kind: graph.KindFunction, Name: "F",
		FilePath: "main.go", StartLine: 3, EndLine: 3, Language: "go",
		Meta: map[string]any{"semantic_type": "func F() string", "semantic_source": "lsp-prior"},
	})
	// G is fresh — must be hovered and stamped. Carries a non-promoted meta
	// key that has nothing to do with enrichment, standing in for whatever
	// else the graph recorded for this node (e.g. a DI-contract tag).
	g.AddNode(&graph.Node{
		ID: "main.go::G", Kind: graph.KindFunction, Name: "G",
		FilePath: "main.go", StartLine: 5, EndLine: 5, Language: "go",
		Meta: map[string]any{"unrelated_tag": "keep-me"},
	})

	done := make(chan *semantic.EnrichResult, 1)
	go func() {
		res, err := p.Enrich(g, repoRoot)
		require.NoError(t, err)
		done <- res
	}()
	var res *semantic.EnrichResult
	select {
	case res = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Enrich timed out")
	}

	assert.Equal(t, int64(1), hoverCalls.Load(), "only the fresh node is hovered")
	assert.Equal(t, 1, res.HoverCandidates, "post-filter candidate count excludes the stamped node")
	assert.Equal(t, 2, res.SymbolsTotal)
	assert.Equal(t, 2, res.SymbolsCovered)

	// F keeps its prior stamp (a re-hover would have overwritten it).
	fNode := g.GetNode("main.go::F")
	assert.Equal(t, "func F() string", fNode.Meta["semantic_type"])
	assert.Equal(t, "lsp-prior", fNode.Meta["semantic_source"])

	// G is now stamped by this pass, AND its pre-existing non-promoted meta
	// key survived the light-scan candidate's full refetch + AddBatch.
	gNode := g.GetNode("main.go::G")
	assert.Equal(t, "func G() int", gNode.Meta["semantic_type"])
	assert.Equal(t, "lsp-fake-lsp", gNode.Meta["semantic_source"])
	assert.Equal(t, "keep-me", gNode.Meta["unrelated_tag"],
		"non-promoted meta present before enrichment must survive the light-scan round trip")
}
