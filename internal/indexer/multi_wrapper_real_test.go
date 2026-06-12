package indexer

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/search"
)

// TestInlineWrappers_LiveWebAPI exercises the wrapper-inlining path
// against the actual web/lib/api.ts content from the tuck fixture
// on disk. It's skipped in CI when the file isn't present, but
// locally it's the tightest feedback loop for "why doesn't the live
// daemon produce inlined contracts?" — if this passes and the
// daemon doesn't, something outside the extractor is wrong.
func TestInlineWrappers_LiveWebAPI(t *testing.T) {
	const liveAPIPath = "/Users/zzet/code/my/tuck/web/lib/api.ts"
	info, err := os.Stat(liveAPIPath)
	if err != nil || info.IsDir() {
		t.Skipf("live fixture not available: %v", err)
	}
	src, err := os.ReadFile(liveAPIPath)
	require.NoError(t, err)

	dir := filepath.Join(t.TempDir(), "web")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "lib"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"name":"web","version":"0.0.0"}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "lib", "api.ts"), src, 0o644))
	// Stub types.ts so the TS parser doesn't fail on missing type
	// imports — the extractor doesn't care about type errors, but
	// some parsers still emit warnings.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "lib", "types.ts"),
		[]byte("export type Tuck = any;\n"), 0o644))

	// Also create a stub second repo so the MultiIndexer enters
	// multi-repo mode (prefixed node IDs and FilePaths). That's
	// the real-world shape, and wrapper inlining needs SourceReader
	// to resolve prefix + root correctly.
	dir2 := filepath.Join(t.TempDir(), "other")
	require.NoError(t, os.MkdirAll(dir2, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir2, "go.mod"),
		[]byte("module example.com/other\n\ngo 1.21\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir2, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"), 0o644))

	tmpCfg := filepath.Join(t.TempDir(), "config.yaml")
	gc := &config.GlobalConfig{
		Repos: []config.RepoEntry{
			{Path: dir, Name: "web"},
			{Path: dir2, Name: "other"},
		},
	}
	gc.SetConfigPath(tmpCfg)
	require.NoError(t, gc.Save())
	cm, err := config.NewConfigManager(tmpCfg)
	require.NoError(t, err)

	g := graph.New()
	mi := NewMultiIndexer(g, newMultiLangRegistry(), search.NewBM25(), cm, zap.NewNop())
	_, err = mi.TrackRepoCtx(context.Background(), config.RepoEntry{Path: dir, Name: "web"})
	require.NoError(t, err)
	_, err = mi.TrackRepoCtx(context.Background(), config.RepoEntry{Path: dir2, Name: "other"})
	require.NoError(t, err)

	// At this point ReconcileContractEdges has run. Look for
	// inlined wrapper contracts — if the feature works, there
	// should be many (one per fetchX/createX/etc. export).
	var inlined []string
	for _, n := range g.AllNodes() {
		if n.Kind != graph.KindContract {
			continue
		}
		// Inlined contracts have the wrapper edge pattern — walk
		// consumer edges from each contract and check if the symbol
		// is a top-level api.ts function.
	}
	_ = inlined

	// Dump what we actually have in api.ts (either prefixed or not)
	t.Logf("== api.ts symbols ==")
	for _, n := range g.AllNodes() {
		if (n.FilePath == "lib/api.ts" || n.FilePath == "web/lib/api.ts") && n.Kind != graph.KindFile {
			t.Logf("  %s: %s (kind=%s repo=%s)", n.Kind, n.ID, n.Kind, n.RepoPrefix)
		}
	}

	// Simpler probe: count consumer contracts anchored on exported
	// api functions. Before inlining, the only consumer with a
	// SymbolID is doFetch (the wrapper itself); after inlining,
	// each fetchX/createX/etc. should have one.
	expectedInlinedBySymbol := map[string]bool{}
	for _, n := range g.AllNodes() {
		if (n.Kind == graph.KindFunction || n.Kind == graph.KindMethod) &&
			(n.FilePath == "lib/api.ts" || n.FilePath == "web/lib/api.ts") &&
			n.Name != "doFetch" && n.Name != "request" && n.Name != "parseError" {
			expectedInlinedBySymbol[n.ID] = false
		}
	}
	t.Logf("api.ts functions to inline: %d (excluding doFetch/request/parseError)", len(expectedInlinedBySymbol))

	var anchored []string
	for _, e := range g.AllEdges() {
		if e.Kind != graph.EdgeConsumes {
			continue
		}
		toNode := g.GetNode(e.To)
		if toNode == nil || toNode.Kind != graph.KindContract {
			continue
		}
		typeMeta, _ := toNode.Meta["type"].(string)
		if typeMeta != "http" {
			continue
		}
		if _, ok := expectedInlinedBySymbol[e.From]; ok {
			expectedInlinedBySymbol[e.From] = true
			anchored = append(anchored, e.From+" → "+e.To)
		}
	}

	inlinedCount := 0
	for _, v := range expectedInlinedBySymbol {
		if v {
			inlinedCount++
		}
	}

	t.Logf("inlined HTTP consumer contracts anchored on api.ts functions: %d / %d", inlinedCount, len(expectedInlinedBySymbol))
	for _, a := range anchored {
		t.Logf("  %s", a)
	}
	if inlinedCount == 0 {
		// Dump all EdgeConsumes for web so we can see what DID form.
		for _, e := range g.AllEdges() {
			if e.Kind != graph.EdgeConsumes {
				continue
			}
			from := g.GetNode(e.From)
			if from == nil || from.RepoPrefix != "web" {
				continue
			}
			t.Logf("  EdgeConsumes: %s → %s", e.From, e.To)
		}
	}
	// Hard assert: at least half the api functions should have
	// inlined contracts. Real api.ts has 25+ exports; the wrapper
	// chain is straightforward. If fewer than 5 get inlined
	// something is broken in the pipeline.
	assert.GreaterOrEqual(t, inlinedCount, 5,
		"expected >=5 inlined HTTP consumer contracts from api.ts exports; got %d", inlinedCount)

	// Silence "declared and not used" for io import if we stop using it.
	_ = io.EOF
}
