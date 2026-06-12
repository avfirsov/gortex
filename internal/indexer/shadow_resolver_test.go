package indexer

import (
	"context"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_sqlite"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
)

// TestShadowSwap_ResolverFollowsGraphPointer guards against the regression
// where the indexer's in-memory shadow swap reassigned idx.graph but left
// idx.resolver pointing at the empty disk Store. The symptom was that
// every resolver pass (module attribution, relative imports, edge in-place
// resolution, ...) silently no-op'd for any backend that opted into the
// shadow swap — because the resolver's r.graph.EdgesWithUnresolvedTarget()
// returned 0 against the empty disk store and ResolveAll short-circuited
// on len(pending) == 0.
//
// The test indexes the same Python project twice — once into an in-memory
// *Graph (no shadow swap), once into a sqlite *Store (shadow swap engaged)
// — and asserts both produce the same node ID set and the same module
// attribution output (KindModule nodes for pypi imports).
func TestShadowSwap_ResolverFollowsGraphPointer(t *testing.T) {
	dir := t.TempDir()

	// A pyproject.toml so the dep scanner discovers pypi:requests as
	// an external dependency, which the resolver then materialises as
	// a KindModule node via attributeNonGoModuleImports.
	writeFile(t, filepath.Join(dir, "pyproject.toml"), `
[project]
name = "regression"
dependencies = ["requests>=2.0"]
`)

	// Source file imports the pypi package and a stdlib module. Both
	// flow through the same module-attribution pass.
	writeFile(t, filepath.Join(dir, "app.py"), `
import os
import requests

def fetch(url):
    return requests.get(url).text
`)

	newIdx := func(t *testing.T, g graph.Store) *Indexer {
		t.Helper()
		reg := parser.NewRegistry()
		reg.Register(languages.NewPythonExtractor())
		cfg := config.Default().Index
		cfg.Workers = 2
		return New(g, reg, cfg, zap.NewNop())
	}

	indexAndCollect := func(t *testing.T, g graph.Store) map[string]string {
		t.Helper()
		_, err := newIdx(t, g).IndexCtx(context.Background(), dir)
		require.NoError(t, err)
		ids := map[string]string{}
		for _, n := range g.AllNodes() {
			ids[n.ID] = string(n.Kind)
		}
		return ids
	}

	memG := graph.New()
	memIDs := indexAndCollect(t, memG)

	sqliteDir := t.TempDir()
	sqliteStore, err := store_sqlite.Open(filepath.Join(sqliteDir, "store.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqliteStore.Close() })

	// Sanity: sqlite implements BulkLoader so the shadow swap engages.
	_, isBulk := graph.Store(sqliteStore).(graph.BulkLoader)
	require.True(t, isBulk, "sqlite must implement BulkLoader for this regression to exercise the shadow swap")

	dskIDs := indexAndCollect(t, sqliteStore)

	// The KindModule node the resolver materialises for `import requests`
	// is the canary — without the fix it never gets written, because
	// ResolveAll short-circuits before attributeNonGoModuleImports runs.
	require.Contains(t, memIDs, "module::pypi:requests",
		"baseline: in-memory backend must materialise the pypi module node")
	assert.Contains(t, dskIDs, "module::pypi:requests",
		"shadow-swap path must materialise the pypi module node — regression: resolver pointed at empty disk store")

	// Stdlib import gets the same treatment.
	require.Contains(t, memIDs, "module::python:stdlib::os",
		"baseline: in-memory backend must materialise the python stdlib module node")
	assert.Contains(t, dskIDs, "module::python:stdlib::os",
		"shadow-swap path must materialise the python stdlib module node")

	// Beyond the canary, both backends must produce the same set of
	// node IDs. Any divergence means some resolver pass is still missing
	// from one of the two paths.
	onlyMem := setDiff(memIDs, dskIDs)
	onlyDsk := setDiff(dskIDs, memIDs)
	sort.Strings(onlyMem)
	sort.Strings(onlyDsk)
	assert.Empty(t, onlyMem, "nodes only in memory: %v", onlyMem)
	assert.Empty(t, onlyDsk, "nodes only in sqlite: %v", onlyDsk)
}

func setDiff(a, b map[string]string) []string {
	out := []string{}
	for id := range a {
		if _, ok := b[id]; !ok {
			out = append(out, id)
		}
	}
	return out
}
