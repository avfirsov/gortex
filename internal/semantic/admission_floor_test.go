package semantic

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/graph"
)

// floorManager builds a Manager with one provider per language, each flipping
// its ran flag, so tests can assert which languages the admission floor let
// through.
func floorManager(t *testing.T, langs ...string) (*Manager, map[string]*bool) {
	t.Helper()
	cfg := Config{Enabled: true}
	for i, lang := range langs {
		cfg.Providers = append(cfg.Providers, ProviderConfig{
			Name: "test-" + lang, Languages: []string{lang}, Priority: i + 1, Enabled: true,
		})
	}
	mgr := NewManager(cfg, zap.NewNop())
	ran := make(map[string]*bool, len(langs))
	for _, lang := range langs {
		lang := lang
		flag := new(bool)
		ran[lang] = flag
		mgr.RegisterProvider(&mockProvider{
			name:      "test-" + lang,
			languages: []string{lang},
			available: true,
			enrichFunc: func(graph.Store, string) (*EnrichResult, error) {
				*flag = true
				return &EnrichResult{Provider: "test-" + lang, Language: lang}, nil
			},
		})
	}
	return mgr, ran
}

func addFloorNodes(g *graph.Graph, repo, lang, dir string, kind graph.NodeKind, n int) {
	for i := 0; i < n; i++ {
		g.AddNode(&graph.Node{
			ID:         fmt.Sprintf("%s::%s::%s::%d", repo, lang, dir, i),
			Kind:       kind,
			Name:       fmt.Sprintf("sym%d", i),
			FilePath:   fmt.Sprintf("%s/file%d.%s", dir, i, lang),
			Language:   lang,
			RepoPrefix: repo,
		})
	}
}

// TestEnrichAllAdmissionFloorSkipsVestigialLanguage: a language below the
// floor is not enriched; the dominant language still is; a zero floor keeps
// the old admit-on-any-node behaviour.
func TestEnrichAllAdmissionFloorSkipsVestigialLanguage(t *testing.T) {
	g := graph.New()
	addFloorNodes(g, "myrepo", "go", "internal/app", graph.KindFunction, 30)
	addFloorNodes(g, "myrepo", "python", "scripts", graph.KindFunction, 3)
	roots := map[string]string{"myrepo": t.TempDir()}

	mgr, ran := floorManager(t, "go", "python")
	_, _, err := mgr.EnrichAll(g, roots, EnrichOptions{MinLanguageNodes: 16})
	require.NoError(t, err)
	assert.True(t, *ran["go"], "dominant language must be enriched")
	assert.False(t, *ran["python"], "below-floor language must be skipped")

	mgr, ran = floorManager(t, "go", "python")
	_, _, err = mgr.EnrichAll(g, roots, EnrichOptions{})
	require.NoError(t, err)
	assert.True(t, *ran["go"])
	assert.True(t, *ran["python"], "zero floor must keep presence-only admission")
}

// TestEnrichAllAdmissionFloorIgnoresFixtureAndModuleNodes: fixture corpora and
// dependency module nodes are not census evidence, so a repo whose only Go is
// parser fixtures plus a manifest's module rows admits no Go pass even when
// their raw count clears the floor.
func TestEnrichAllAdmissionFloorIgnoresFixtureAndModuleNodes(t *testing.T) {
	g := graph.New()
	addFloorNodes(g, "myrepo", "rust", "src", graph.KindFunction, 40)
	addFloorNodes(g, "myrepo", "go", "test/resources/repos/go", graph.KindFunction, 25)
	addFloorNodes(g, "myrepo", "go", "bench/fixtures", graph.KindFunction, 25)
	addFloorNodes(g, "myrepo", "go", "deps", graph.KindModule, 25)
	roots := map[string]string{"myrepo": t.TempDir()}

	mgr, ran := floorManager(t, "rust", "go")
	_, _, err := mgr.EnrichAll(g, roots, EnrichOptions{MinLanguageNodes: 16})
	require.NoError(t, err)
	assert.True(t, *ran["rust"], "real source language must be enriched")
	assert.False(t, *ran["go"], "fixture corpora and module rows must not admit a Go pass")
}

// TestEnrichmentAdmissionFloorEnv pins the env override contract.
func TestEnrichmentAdmissionFloorEnv(t *testing.T) {
	t.Setenv("GORTEX_ENRICH_MIN_NODES", "")
	assert.Equal(t, defaultEnrichmentAdmissionFloor, EnrichmentAdmissionFloor())
	t.Setenv("GORTEX_ENRICH_MIN_NODES", "48")
	assert.Equal(t, 48, EnrichmentAdmissionFloor())
	t.Setenv("GORTEX_ENRICH_MIN_NODES", "-1")
	assert.Equal(t, 0, EnrichmentAdmissionFloor(), "negative disables the floor")
	t.Setenv("GORTEX_ENRICH_MIN_NODES", "junk")
	assert.Equal(t, defaultEnrichmentAdmissionFloor, EnrichmentAdmissionFloor())
}
