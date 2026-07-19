package goanalysis

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/tools/go/packages"

	"github.com/zzet/gortex/internal/graph/store_sqlite"
)

// The loadability probe exists for the doomed shape only (manifest below the
// root, so "./..." from the root is out-of-module). A repo with a root
// go.mod/go.work loads normally, and a second metadata enumeration there is a
// pure per-repo tax — the probe must not run.
func TestEnrichProbeGatedToNonRootManifests(t *testing.T) {
	t.Setenv("GORTEX_GOTYPES_PROBE_TIMEOUT", "30s")

	store, err := store_sqlite.Open(filepath.Join(t.TempDir(), "graph.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	var modes []packages.LoadMode
	provider := NewProvider(ModeTypeCheck, false, zap.NewNop())
	t.Cleanup(func() { require.NoError(t, provider.Close()) })
	provider.packagesLoad = func(cfg *packages.Config, patterns ...string) ([]*packages.Package, error) {
		modes = append(modes, cfg.Mode)
		return []*packages.Package{{Errors: []packages.Error{{Msg: "canned"}}}}, nil
	}

	// Mode shapes: the probe is NeedName|NeedFiles (neither NeedTypes nor
	// NeedDeps); the dependency metadata index carries NeedDeps without
	// NeedTypes; the main export-data load carries NeedTypes without
	// NeedDeps.
	isProbe := func(m packages.LoadMode) bool {
		return m&packages.NeedTypes == 0 && m&packages.NeedDeps == 0
	}

	t.Run("root manifest skips the probe", func(t *testing.T) {
		root := resolvedTempDir(t)
		writeGoMod(t, root, "example.com/rooted")
		writeFile(t, root, "main.go", "package rooted\n")

		modes = nil
		_, err := provider.EnrichRepo(store, "", root)
		require.NoError(t, err)
		typed := 0
		for _, m := range modes {
			require.False(t, isProbe(m),
				"no probe load may run for a root-manifest repo")
			if m&packages.NeedTypes != 0 {
				typed++
			}
		}
		require.Equal(t, 1, typed, "exactly one typed main load must run")
	})

	t.Run("subdir manifest probes and fails closed", func(t *testing.T) {
		root := resolvedTempDir(t)
		require.NoError(t, os.MkdirAll(filepath.Join(root, "backend"), 0o755))
		writeGoMod(t, filepath.Join(root, "backend"), "example.com/buried")
		writeFile(t, root, "backend/main.go", "package buried\n")

		modes = nil
		res, err := provider.EnrichRepo(store, "", root)
		require.NoError(t, err)
		require.Len(t, modes, 1, "exactly the probe load must run")
		require.True(t, isProbe(modes[0]), "the probe is metadata-only")
		require.True(t, res.Degraded, "an unloadable buried module is skipped before the heavy load")
	})
}
