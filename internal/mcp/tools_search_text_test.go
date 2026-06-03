package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/indexer"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
	"github.com/zzet/gortex/internal/query"
	"github.com/zzet/gortex/internal/search"
)

type searchTextMatch struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

func TestSearchText(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package app\n\nfunc Alpha() {}\n\nfunc Beta() { Alpha() }\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "other.go"),
		[]byte("package app\n\nfunc Gamma() {}\n"), 0o644))

	g := graph.New()
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	cfg := config.Default()
	idx := indexer.New(g, reg, cfg.Index, zap.NewNop())
	_, err := idx.Index(dir)
	require.NoError(t, err)
	eng := query.NewEngine(g)
	srv := NewServer(eng, g, idx, nil, zap.NewNop(), nil)

	decode := func(res *mcplib.CallToolResult) struct {
		Matches []searchTextMatch `json:"matches"`
		Count   int               `json:"count"`
	} {
		require.False(t, res.IsError)
		var out struct {
			Matches []searchTextMatch `json:"matches"`
			Count   int               `json:"count"`
		}
		require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &out))
		return out
	}

	// A literal present in exactly one file.
	resp := decode(callTool(t, srv, "search_text", map[string]any{"query": "func Beta"}))
	require.Equal(t, 1, resp.Count)
	require.Equal(t, "main.go", resp.Matches[0].Path)
	require.Equal(t, 5, resp.Matches[0].Line)

	// A literal present nowhere.
	none := decode(callTool(t, srv, "search_text", map[string]any{"query": "zzz_absent_literal"}))
	require.Equal(t, 0, none.Count)

	// The limit argument caps the result set.
	limited := decode(callTool(t, srv, "search_text",
		map[string]any{"query": "package app", "limit": 1}))
	require.Equal(t, 1, limited.Count)

	// An empty query is a tool error.
	bad := callTool(t, srv, "search_text", map[string]any{})
	require.True(t, bad.IsError)
}

// TestSearchText_EnclosingSymbol confirms each literal hit is
// decorated with the graph symbol that encloses the matching line.
func TestSearchText_EnclosingSymbol(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package app\n\nfunc Alpha() {\n\tprintln(\"needle_here\")\n}\n"), 0o644))

	g := graph.New()
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	cfg := config.Default()
	idx := indexer.New(g, reg, cfg.Index, zap.NewNop())
	_, err := idx.Index(dir)
	require.NoError(t, err)
	eng := query.NewEngine(g)
	srv := NewServer(eng, g, idx, nil, zap.NewNop(), nil)

	res := callTool(t, srv, "search_text", map[string]any{"query": "needle_here"})
	require.False(t, res.IsError)
	var out struct {
		Matches []struct {
			Path       string `json:"path"`
			Line       int    `json:"line"`
			SymbolID   string `json:"symbol_id"`
			SymbolName string `json:"symbol_name"`
		} `json:"matches"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &out))
	require.Len(t, out.Matches, 1)
	require.Equal(t, "Alpha", out.Matches[0].SymbolName,
		"the literal lands inside func Alpha -- search_text should report it as the enclosing symbol")
	require.NotEmpty(t, out.Matches[0].SymbolID)
}

// TestSearchText_PathScoping confirms the path argument confines the
// literal-search hits to the named sub-path.
func TestSearchText_PathScoping(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "services", "billing"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "services", "auth"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "services", "billing", "b.go"),
		[]byte("package billing\n\n// shared_marker here\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "services", "auth", "a.go"),
		[]byte("package auth\n\n// shared_marker here\n"), 0o644))

	g := graph.New()
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	cfg := config.Default()
	idx := indexer.New(g, reg, cfg.Index, zap.NewNop())
	_, err := idx.Index(dir)
	require.NoError(t, err)
	eng := query.NewEngine(g)
	srv := NewServer(eng, g, idx, nil, zap.NewNop(), nil)

	// Without a path filter both files match.
	all := callTool(t, srv, "search_text", map[string]any{"query": "shared_marker"})
	require.False(t, all.IsError)
	var allOut struct {
		Matches []struct {
			Path string `json:"path"`
		} `json:"matches"`
	}
	require.NoError(t, json.Unmarshal([]byte(all.Content[0].(mcplib.TextContent).Text), &allOut))
	require.Len(t, allOut.Matches, 2)

	// With a path filter only the billing file matches.
	scoped := callTool(t, srv, "search_text",
		map[string]any{"query": "shared_marker", "path": "services/billing"})
	require.False(t, scoped.IsError)
	var scopedOut struct {
		Matches []struct {
			Path string `json:"path"`
		} `json:"matches"`
	}
	require.NoError(t, json.Unmarshal([]byte(scoped.Content[0].(mcplib.TextContent).Text), &scopedOut))
	require.Len(t, scopedOut.Matches, 1)
	require.Contains(t, scopedOut.Matches[0].Path, "services/billing")
}

// TestSearchText_MultiRepoFanout pins the multi-repo path: when the
// server holds a MultiIndexer, search_text must fan out across every
// tracked per-repo trigram searcher and re-prefix match paths so
// downstream tools (resolveGraphPath, path filters) see the same
// repo-prefixed shape graph nodes use. Before the fix, the handler
// only consulted s.indexer.GrepText — which in multi-repo mode has no
// rootPath and silently returned an empty result for every query.
func TestSearchText_MultiRepoFanout(t *testing.T) {
	repoA := setupMiniRepoNamed(t, "alpha", "package alpha\n\n// unique_alpha_marker\nfunc A() {}\n")
	repoB := setupMiniRepoNamed(t, "beta", "package beta\n\n// unique_beta_marker\nfunc B() {}\n")

	tmpCfg := filepath.Join(t.TempDir(), "config.yaml")
	gc := &config.GlobalConfig{
		Repos: []config.RepoEntry{
			{Path: repoA, Name: "alpha"},
			{Path: repoB, Name: "beta"},
		},
	}
	gc.SetConfigPath(tmpCfg)
	require.NoError(t, gc.Save())
	cm, err := config.NewConfigManager(tmpCfg)
	require.NoError(t, err)

	reg := parser.NewRegistry()
	reg.Register(languages.NewGoExtractor())
	g := graph.New()
	mi := indexer.NewMultiIndexer(g, reg, search.NewBM25(), cm, zap.NewNop())
	_, err = mi.IndexAll()
	require.NoError(t, err)
	require.True(t, mi.IsMultiRepo())

	eng := query.NewEngine(g)
	singleton := indexer.New(g, reg, config.IndexConfig{}, zap.NewNop())
	srv := NewServer(eng, g, singleton, nil, zap.NewNop(), nil, MultiRepoOptions{
		ConfigManager: cm,
		MultiIndexer:  mi,
	})

	// Each repo's unique marker must surface, prefixed with the repo name.
	alpha := callTool(t, srv, "search_text", map[string]any{"query": "unique_alpha_marker"})
	require.False(t, alpha.IsError)
	var alphaOut struct {
		Matches []searchTextMatch `json:"matches"`
		Count   int               `json:"count"`
	}
	require.NoError(t, json.Unmarshal([]byte(alpha.Content[0].(mcplib.TextContent).Text), &alphaOut))
	require.Equal(t, 1, alphaOut.Count, "alpha marker must surface; pre-fix it returned 0")
	require.True(t, strings.HasPrefix(alphaOut.Matches[0].Path, "alpha/"),
		"match path must be repo-prefixed, got %q", alphaOut.Matches[0].Path)

	beta := callTool(t, srv, "search_text", map[string]any{"query": "unique_beta_marker"})
	require.False(t, beta.IsError)
	var betaOut struct {
		Matches []searchTextMatch `json:"matches"`
		Count   int               `json:"count"`
	}
	require.NoError(t, json.Unmarshal([]byte(beta.Content[0].(mcplib.TextContent).Text), &betaOut))
	require.Equal(t, 1, betaOut.Count)
	require.True(t, strings.HasPrefix(betaOut.Matches[0].Path, "beta/"))
}

// setupMiniRepoNamed mirrors setupMiniRepo but lets the caller supply
// the main.go body so each repo carries a distinct literal the search
// can latch onto.
func setupMiniRepoNamed(t *testing.T, name, body string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(body), 0o644))
	return dir
}

// TestSearchText_Regexp covers the regexp mode: the same query runs
// through the trigram backbone as a compiled regular expression, an
// alternation matches multiple sites a literal never would, and a bad
// pattern surfaces as a tool error rather than zero hits.
func TestSearchText_Regexp(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package app\n\nfunc HandleAlpha() {}\n\nfunc HandleBeta() {}\n\nfunc Other() {}\n"), 0o644))

	g := graph.New()
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	cfg := config.Default()
	idx := indexer.New(g, reg, cfg.Index, zap.NewNop())
	_, err := idx.Index(dir)
	require.NoError(t, err)
	eng := query.NewEngine(g)
	srv := NewServer(eng, g, idx, nil, zap.NewNop(), nil)

	decode := func(res *mcplib.CallToolResult) int {
		require.False(t, res.IsError)
		var out struct {
			Count int `json:"count"`
		}
		require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &out))
		return out.Count
	}

	// The alternation matches both Handle* funcs but not Other.
	got := decode(callTool(t, srv, "search_text",
		map[string]any{"query": "func Handle(Alpha|Beta)", "regexp": true}))
	require.Equal(t, 2, got)

	// The same string as a literal matches nothing.
	lit := decode(callTool(t, srv, "search_text",
		map[string]any{"query": "func Handle(Alpha|Beta)"}))
	require.Equal(t, 0, lit)

	// An invalid pattern is a tool error, not a silent empty result.
	bad := callTool(t, srv, "search_text",
		map[string]any{"query": "func Handle(", "regexp": true})
	require.True(t, bad.IsError)
}
