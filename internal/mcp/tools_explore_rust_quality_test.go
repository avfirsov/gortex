package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph/store_sqlite"
	"github.com/zzet/gortex/internal/indexer"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
	"github.com/zzet/gortex/internal/query"
)

func newIndexedRustLocalizationServer(t *testing.T) func() *Server {
	t.Helper()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src", "dir.rs"), []byte(`
pub struct Ignore;
impl Ignore {
    pub fn matched_ignore(&self, path: &Path) -> bool {
        if path == Path::new(".") {
            return self.match_path(path);
        }
        self.match_path(path) || self.add_parents(path)
    }

    fn match_path(&self, path: &Path) -> bool { path.is_relative() }
    fn add_parents(&self, path: &Path) -> bool { path.parent().is_some() }
}
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src", "builder.rs"), []byte(`
pub struct StandardBuilder;
pub struct SummaryBuilder;
pub struct WalkBuilder;

impl StandardBuilder { pub fn path(&self, path: &Path) -> bool { path.exists() } }
impl SummaryBuilder { pub fn path(&self, path: &Path) -> bool { path.exists() } }
impl WalkBuilder {
    pub fn path(&self, path: &Path) -> bool { self.parents(path) }
    fn parents(&self, path: &Path) -> bool { self.ancestors(path) }
    fn ancestors(&self, path: &Path) -> bool { path.parent().is_some() }
}
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src", "paths.rs"), []byte(`
pub struct Paths;
pub struct HiArgs;

impl Paths {
    pub fn path(&self, path: &Path) -> bool { path.exists() }
    pub fn has_implicit_path(&self) -> bool { false }
    pub fn from_patterns(&self, patterns: &[String]) -> bool { !patterns.is_empty() }
}
impl HiArgs {
    pub fn no_ignore_files(&self) -> bool { false }
    pub fn no_ignore_exclude(&self) -> bool { false }
    pub fn explicit_path(&self, path: &Path) -> bool { path.exists() }
}
`), 0o644))

	store, err := store_sqlite.Open(filepath.Join(t.TempDir(), "graph.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	registry := parser.NewRegistry()
	registry.Register(languages.NewRustExtractor())
	idx := indexer.New(store, registry, config.IndexConfig{Workers: 1}, zap.NewNop())
	idx.SetRepoPrefix("rust-fixture")
	idx.SetWorkspaceID("rust-fixture")
	idx.SetProjectID("rust-fixture")
	_, err = idx.IndexCtx(context.Background(), root)
	require.NoError(t, err)

	return func() *Server {
		engine := query.NewEngine(store)
		engine.SetSearchProvider(idx.Search)
		return NewServer(engine, store, idx, nil, zap.NewNop(), nil)
	}
}

func callIndexedRustLocalization(t *testing.T, server *Server, task string) localizationExploreEnvelope {
	t.Helper()

	request := mcpgo.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"task":          task,
		"localize":      true,
		"max_symbols":   5,
		"token_budget":  1600,
		"repository_id": "rust-fixture",
	}
	result, err := server.handleExplore(context.Background(), request)
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.NotEmpty(t, result.Content)

	text, ok := result.Content[0].(mcpgo.TextContent)
	require.True(t, ok)
	var envelope localizationExploreEnvelope
	require.NoError(t, json.Unmarshal([]byte(text.Text), &envelope))
	return envelope
}

func TestIndexedRustLocalizationRetainsMatchedIgnoreForShortAndLongParaphrases(t *testing.T) {
	newServer := newIndexedRustLocalizationServer(t)
	tests := []struct {
		name string
		task string
	}{
		{name: "short", task: "Find the callable that matches ancestor ignore rules for an explicit dot path."},
		{name: "long", task: exploreLongIgnoreTask},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envelope := callIndexedRustLocalization(t, newServer(), tt.task)
			found := false
			for _, evidence := range envelope.Evidence {
				if evidence.Name == "matched_ignore" && filepath.Base(evidence.File) == "dir.rs" {
					found = true
					break
				}
			}
			require.True(t, found, "expected matched_ignore in dir.rs, got %#v", envelope.Evidence)
			require.NotEqual(t, "continue", envelope.Completion.RequiredAction)
		})
	}
}
