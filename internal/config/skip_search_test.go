package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShouldSkipSearch_Matches(t *testing.T) {
	rules := []SkipEmbedRule{
		{Language: "json", Kinds: []string{"variable"}},
		{Language: "yaml", Kinds: []string{"variable"}},
	}
	cases := []struct {
		lang, kind string
		want       bool
	}{
		{"json", "variable", true},
		{"json", "function", false},
		{"yaml", "variable", true},
		{"yaml", "type", false},
		{"go", "function", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got := ShouldSkipSearch(rules, tc.lang, tc.kind)
		if got != tc.want {
			t.Errorf("ShouldSkipSearch(%q,%q) = %v, want %v", tc.lang, tc.kind, got, tc.want)
		}
	}
}

func TestShouldSkipSearch_NilRules(t *testing.T) {
	if ShouldSkipSearch(nil, "json", "variable") {
		t.Error("nil rule list should match nothing")
	}
}

// DefaultSkipSearch must be a superset of DefaultSkipEmbed and must
// include json:variable — without that, the search-index blowup on
// monorepos with big package.json / tsconfig trees comes back.
func TestDefaultSkipSearch_IsSupersetOfSkipEmbed(t *testing.T) {
	search := DefaultSkipSearch()
	has := func(lang, kind string) bool {
		return ShouldSkipSearch(search, lang, kind)
	}
	for _, rule := range DefaultSkipEmbed() {
		for _, kind := range rule.Kinds {
			assert.True(t, has(rule.Language, kind),
				"DefaultSkipSearch missing (%s, %s) — must be superset of DefaultSkipEmbed",
				rule.Language, kind)
		}
	}
}

func TestDefaultSkipSearch_CoversJSONAndConfigKinds(t *testing.T) {
	rules := DefaultSkipSearch()
	// Pin the additions that made this filter necessary in the first
	// place. Regressions here are the exact bug we shipped this fix for.
	wants := []struct{ lang, kind string }{
		{"json", "variable"},
		{"liquid", "variable"},
		{"jinja", "variable"},
		{"markdown", "variable"},
		{"makefile", "variable"},
		{"dockerfile", "variable"},
	}
	for _, w := range wants {
		if !ShouldSkipSearch(rules, w.lang, w.kind) {
			t.Errorf("DefaultSkipSearch missing (%q,%q)", w.lang, w.kind)
		}
	}
}

func TestGetRepoConfig_SkipSearchFallsBackToDefault(t *testing.T) {
	cm, err := NewConfigManager("/tmp/nonexistent-gortex-test-cm-search/config.yaml")
	require.NoError(t, err)

	cfg := cm.GetRepoConfig("unknown-repo")
	assert.NotEmpty(t, cfg.Index.SkipSearch, "Index.SkipSearch should be populated for indexer")
	assert.NotEmpty(t, cfg.Semantic.SkipSearch, "Semantic.SkipSearch should carry the defaults")
}

func TestGetRepoConfig_SkipSearchFromWorkspace(t *testing.T) {
	cm, err := NewConfigManager("/tmp/nonexistent-gortex-test-cm-search/config.yaml")
	require.NoError(t, err)

	repoDir := t.TempDir()
	wsContent := `
semantic:
  skip_search:
    - language: foo
      kinds: [bar]
`
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, ".gortex.yaml"), []byte(wsContent), 0644))
	cm.LoadWorkspaceConfig("r", repoDir)

	cfg := cm.GetRepoConfig("r")
	require.Len(t, cfg.Index.SkipSearch, 1)
	assert.Equal(t, "foo", cfg.Index.SkipSearch[0].Language)
	assert.Equal(t, []string{"bar"}, cfg.Index.SkipSearch[0].Kinds)
}
