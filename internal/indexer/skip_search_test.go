package indexer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
)

// Indexing a directory with JSON + Go files must keep JSON keys out
// of the text search index by default (regression guard for the
// search-backend memory blowup) while still leaving them reachable
// via graph queries. Uses lowercase-only single-word identifiers so
// the BM25 query tokenizer (which does not split camelCase) hits
// exactly the tokens produced by the Add-path tokenizer.
func TestIndex_JSONVariablesSkippedFromSearchByDefault(t *testing.T) {
	dir := t.TempDir()

	jsonPath := filepath.Join(dir, "package.json")
	require.NoError(t, os.WriteFile(jsonPath, []byte(`{
  "uniquejsonkeyzzz": "value"
}`), 0o644))

	goPath := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(goPath, []byte(`package main

func uniquegosymbolzzz() {}
`), 0o644))

	g := graph.New()

	reg := parser.NewRegistry()
	reg.Register(languages.NewGoExtractor())
	reg.Register(languages.NewJSONExtractor())

	cfg := config.Default().Index
	cfg.Workers = 1
	// Mirror what ConfigManager.GetRepoConfig would do for a real run.
	cfg.SkipSearch = config.DefaultSkipSearch()

	idx := New(g, reg, cfg, zap.NewNop())
	_, err := idx.Index(dir)
	require.NoError(t, err)

	// Graph still carries the JSON key — SkipSearch is about the text
	// index, not the graph. Users looking up a config key via
	// FindNodesByName / get_symbol must still find it.
	jsonNodes := g.FindNodesByName("uniquejsonkeyzzz")
	require.NotEmpty(t, jsonNodes, "JSON variable node should exist in graph")

	// Search index must include the Go symbol but NOT the JSON key.
	goHits := idx.Search().Search("uniquegosymbolzzz", 10)
	assert.NotEmpty(t, goHits, "Go symbol should be text-indexed")

	jsonHits := idx.Search().Search("uniquejsonkeyzzz", 10)
	assert.Empty(t, jsonHits,
		"JSON variable node must be excluded from text index by default (SkipSearch)")
}

// With SkipSearch cleared, JSON variable nodes are text-searchable
// again. Guards the config surface: users who actually want to search
// their package.json keys can opt in by overriding SkipSearch.
func TestIndex_JSONVariablesSearchableWhenSkipSearchCleared(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "overridekeyzzz": "value"
}`), 0o644))

	g := graph.New()

	reg := parser.NewRegistry()
	reg.Register(languages.NewJSONExtractor())

	cfg := config.Default().Index
	cfg.Workers = 1
	cfg.SkipSearch = nil // override: index everything

	idx := New(g, reg, cfg, zap.NewNop())
	_, err := idx.Index(dir)
	require.NoError(t, err)

	hits := idx.Search().Search("overridekeyzzz", 10)
	assert.NotEmpty(t, hits,
		"JSON variable node should be text-indexed when SkipSearch is cleared")
}
