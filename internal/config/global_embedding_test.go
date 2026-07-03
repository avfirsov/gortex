package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func boolPtr(b bool) *bool { return &b }

func TestLoadGlobal_EmbeddingSectionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`active_project: ""
repos: []
embedding:
    enabled: true
    provider: local
    variant: jina_code
    max_symbols: 50000
`), 0o644))

	gc, err := LoadGlobal(cfgPath)
	require.NoError(t, err)
	require.NotNil(t, gc)
	require.NotNil(t, gc.Embedding.Enabled)
	assert.True(t, *gc.Embedding.Enabled)
	assert.Equal(t, "local", gc.Embedding.Provider)
	assert.Equal(t, "jina_code", gc.Embedding.Variant)
	assert.Equal(t, 50000, gc.Embedding.MaxSymbols)
}

func TestGlobalConfig_MergeEmbeddingInto_FillsZeroFields(t *testing.T) {
	gc := &GlobalConfig{Embedding: EmbeddingConfig{
		Enabled:        boolPtr(true),
		Provider:       "local",
		Variant:        "bge_small",
		APIConcurrency: 8,
	}}

	got := gc.MergeEmbeddingInto(EmbeddingConfig{})
	require.NotNil(t, got.Enabled)
	assert.True(t, *got.Enabled)
	assert.Equal(t, "local", got.Provider)
	assert.Equal(t, "bge_small", got.Variant)
	assert.Equal(t, 8, got.APIConcurrency)
}

func TestGlobalConfig_MergeEmbeddingInto_LocalWinsPerField(t *testing.T) {
	gc := &GlobalConfig{Embedding: EmbeddingConfig{
		Enabled:  boolPtr(true),
		Provider: "static",
		Variant:  "bge_small",
	}}

	got := gc.MergeEmbeddingInto(EmbeddingConfig{
		Enabled:  boolPtr(false), // explicit local off wins over global on
		Provider: "local",        // local provider wins
		// Variant left empty -> inherits global
	})
	require.NotNil(t, got.Enabled)
	assert.False(t, *got.Enabled, "explicit local Enabled:false must win over global true")
	assert.Equal(t, "local", got.Provider)
	assert.Equal(t, "bge_small", got.Variant, "empty local variant inherits the global one")
}

func TestGlobalConfig_MergeEmbeddingInto_NilReceiver(t *testing.T) {
	var gc *GlobalConfig
	local := EmbeddingConfig{Provider: "api", APIURL: "http://x"}
	got := gc.MergeEmbeddingInto(local)
	assert.Equal(t, local, got, "nil receiver returns local unchanged")
}

func TestUnknownGlobalKeys(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`active_project: ""
llm:
    provider: local
embedding:
    provider: local
embeddings:            # <- typo: the ignored, unrecognised key
    provider: local
totally_unknown: 1
`), 0o644))

	unknown := UnknownGlobalKeys(cfgPath)
	assert.Equal(t, []string{"embeddings", "totally_unknown"}, unknown)

	// The recognised keys still load fine — an unknown key never fails the load.
	gc, err := LoadGlobal(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "local", gc.Embedding.Provider)
}

func TestUnknownGlobalKeys_CleanConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`embedding:
    provider: local
llm:
    provider: local
`), 0o644))
	assert.Empty(t, UnknownGlobalKeys(cfgPath))

	// A missing file is not an error and yields no keys.
	assert.Empty(t, UnknownGlobalKeys(filepath.Join(dir, "does-not-exist.yaml")))
}

func TestGlobalConfig_SaveEmbeddingRoundTrip(t *testing.T) {
	gc := &GlobalConfig{Embedding: EmbeddingConfig{Provider: "local", Variant: "jina_code"}}
	gc.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
	require.NoError(t, gc.Save())

	back, err := LoadGlobal(gc.ConfigPath())
	require.NoError(t, err)
	assert.Equal(t, "local", back.Embedding.Provider)
	assert.Equal(t, "jina_code", back.Embedding.Variant)
}
