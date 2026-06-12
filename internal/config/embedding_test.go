package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeConfig writes content to a .gortex.yaml in a fresh temp dir and
// returns its path, ready for config.Load.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".gortex.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

// TestEmbeddingConfig_AbsentIsDefaultOn asserts that a config with no
// `embedding:` block leaves Enabled nil — which the tri-state resolver
// reads as "semantic search ON" — and selects the static provider.
func TestEmbeddingConfig_AbsentIsDefaultOn(t *testing.T) {
	cfg, err := Load(writeConfig(t, "index:\n  workers: 2\n"))
	require.NoError(t, err)

	assert.Nil(t, cfg.Embedding.Enabled,
		"an absent embedding block must leave Enabled nil (not false) so default-on applies")
	assert.True(t, cfg.Embedding.EmbeddingEnabledOrDefault(),
		"a nil Enabled flag must resolve to ON")
	assert.Equal(t, "static", cfg.Embedding.EmbeddingProviderOrDefault(),
		"the default provider is the zero-download static GloVe backend")
}

// TestEmbeddingConfig_ExplicitlyDisabled asserts that an explicit
// `embedding.enabled: false` is distinguishable from "absent" and
// turns the vector channel off.
func TestEmbeddingConfig_ExplicitlyDisabled(t *testing.T) {
	cfg, err := Load(writeConfig(t, "embedding:\n  enabled: false\n"))
	require.NoError(t, err)

	require.NotNil(t, cfg.Embedding.Enabled, "an explicit value must round-trip as a non-nil pointer")
	assert.False(t, *cfg.Embedding.Enabled)
	assert.False(t, cfg.Embedding.EmbeddingEnabledOrDefault())
}

// TestEmbeddingConfig_ExplicitlyEnabled asserts that `enabled: true`
// round-trips and resolves to ON.
func TestEmbeddingConfig_ExplicitlyEnabled(t *testing.T) {
	cfg, err := Load(writeConfig(t, "embedding:\n  enabled: true\n"))
	require.NoError(t, err)

	require.NotNil(t, cfg.Embedding.Enabled)
	assert.True(t, *cfg.Embedding.Enabled)
	assert.True(t, cfg.Embedding.EmbeddingEnabledOrDefault())
}

// TestEmbeddingConfig_FullBlockRoundTrip asserts every key of the
// embedding block — provider, API settings, and the chunking /
// concurrency knobs — survives a load.
func TestEmbeddingConfig_FullBlockRoundTrip(t *testing.T) {
	yaml := `embedding:
  enabled: true
  provider: api
  api_url: http://localhost:11434
  api_model: nomic-embed-text
  max_symbols: 50000
  chunk_threshold_lines: 80
  chunk_window_lines: 32
  api_concurrency: 8
`
	cfg, err := Load(writeConfig(t, yaml))
	require.NoError(t, err)

	e := cfg.Embedding
	require.NotNil(t, e.Enabled)
	assert.True(t, *e.Enabled)
	assert.Equal(t, "api", e.Provider)
	assert.Equal(t, "http://localhost:11434", e.APIURL)
	assert.Equal(t, "nomic-embed-text", e.APIModel)
	assert.Equal(t, 50000, e.MaxSymbols)
	assert.Equal(t, 80, e.ChunkThresholdLines)
	assert.Equal(t, 32, e.ChunkWindowLines)
	assert.Equal(t, 8, e.APIConcurrency)
}

// TestEmbeddingConfig_DefaultsArePresent asserts config.Default()
// leaves the embedding block as a zero value — Enabled nil — so the
// default-on resolution comes from EmbeddingEnabledOrDefault, not from
// a baked-in pointer.
func TestEmbeddingConfig_DefaultsArePresent(t *testing.T) {
	cfg := Default()
	assert.Nil(t, cfg.Embedding.Enabled)
	assert.Equal(t, "", cfg.Embedding.Provider)
	// The resolver still reports the intended defaults.
	assert.True(t, cfg.Embedding.EmbeddingEnabledOrDefault())
	assert.Equal(t, "static", cfg.Embedding.EmbeddingProviderOrDefault())
}
