package tsalias

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolve_WildcardAlias(t *testing.T) {
	m := &Map{
		Entries: []Alias{
			{AliasPrefix: "@/", TargetPrefix: "src/", HasWildcard: true},
		},
	}
	assert.Equal(t, "src/lib/foo", Resolve(m, "@/lib/foo"))
}

func TestResolve_ExactAlias(t *testing.T) {
	m := &Map{
		Entries: []Alias{
			{AliasPrefix: "$utils", TargetPrefix: "src/util/index.ts"},
		},
	}
	assert.Equal(t, "src/util/index", Resolve(m, "$utils"), "extension stripped")
	assert.Equal(t, "", Resolve(m, "$utils/format"), "exact match doesn't fire on a longer specifier")
}

func TestResolve_BaseURLPrepended(t *testing.T) {
	m := &Map{
		BaseURL: "src",
		Entries: []Alias{
			{AliasPrefix: "@/", TargetPrefix: "", HasWildcard: true},
		},
	}
	assert.Equal(t, "src/lib/foo", Resolve(m, "@/lib/foo"))
}

func TestResolve_LongestPrefixWins(t *testing.T) {
	m := &Map{
		Entries: []Alias{
			{AliasPrefix: "@components/", TargetPrefix: "src/components/", HasWildcard: true},
			{AliasPrefix: "@/", TargetPrefix: "src/", HasWildcard: true},
		},
	}
	// Caller sorts entries; assert that with the sorted order the
	// longer prefix matches.
	assert.Equal(t, "src/components/Button", Resolve(m, "@components/Button"))
	assert.Equal(t, "src/Button", Resolve(m, "@/Button"))
}

func TestResolve_NoMatch(t *testing.T) {
	m := &Map{
		Entries: []Alias{
			{AliasPrefix: "@/", TargetPrefix: "src/", HasWildcard: true},
		},
	}
	assert.Equal(t, "", Resolve(m, "react"))
	assert.Equal(t, "", Resolve(nil, "@/foo"))
}

func TestLoad_FindsTsconfigAtRoot(t *testing.T) {
	dir := t.TempDir()
	cfg := `{
  "compilerOptions": {
    "baseUrl": ".",
    "paths": {
      "@/*": ["src/*"],
      "@app": ["src/app/index.ts"]
    }
  }
}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(cfg), 0644))

	coll := Load(dir)
	require.NotNil(t, coll)
	require.Len(t, coll.Maps(), 1)

	m := coll.FindForFile("src/components/Button.tsx")
	require.NotNil(t, m)
	assert.Equal(t, "src/foo", Resolve(m, "@/foo"))
	assert.Equal(t, "src/app/index", Resolve(m, "@app"))
}

func TestLoad_NestedTsconfigWinsForChildren(t *testing.T) {
	dir := t.TempDir()
	rootCfg := `{
  "compilerOptions": {
    "paths": { "@/*": ["root/*"] }
  }
}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(rootCfg), 0644))

	nestedDir := filepath.Join(dir, "packages", "web")
	require.NoError(t, os.MkdirAll(nestedDir, 0755))
	nestedCfg := `{
  "compilerOptions": {
    "paths": { "@/*": ["nested/*"] }
  }
}`
	require.NoError(t, os.WriteFile(filepath.Join(nestedDir, "tsconfig.json"), []byte(nestedCfg), 0644))

	coll := Load(dir)
	require.NotNil(t, coll)

	// A file under packages/web/ picks up the nested map.
	m := coll.FindForFile("packages/web/src/App.tsx")
	require.NotNil(t, m)
	assert.Equal(t, "packages/web/nested/foo", Resolve(m, "@/foo"),
		"nested config target is rooted at the config's own directory")

	// A file outside packages/web/ falls back to the root map.
	m = coll.FindForFile("apps/api/src/server.ts")
	require.NotNil(t, m)
	assert.Equal(t, "root/foo", Resolve(m, "@/foo"))
}

func TestLoad_SkipsNodeModules(t *testing.T) {
	dir := t.TempDir()
	nm := filepath.Join(dir, "node_modules", "some-pkg")
	require.NoError(t, os.MkdirAll(nm, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(nm, "tsconfig.json"),
		[]byte(`{"compilerOptions":{"paths":{"@/*":["should/not/be/used/*"]}}}`), 0644))

	coll := Load(dir)
	// Either nil (no usable configs found) or a collection with zero
	// entries — both prove the node_modules config was skipped.
	if coll == nil {
		return
	}
	for _, m := range coll.Maps() {
		assert.NotContains(t, m.DirPrefix, "node_modules")
	}
}

func TestLoad_NoConfigsReturnsNil(t *testing.T) {
	dir := t.TempDir()
	assert.Nil(t, Load(dir))
}

func TestLoad_MalformedConfigSkipped(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tsconfig.json"),
		[]byte(`{ this is not json`), 0644))
	// Should not panic; should return nil because no usable config.
	assert.Nil(t, Load(dir))
}
