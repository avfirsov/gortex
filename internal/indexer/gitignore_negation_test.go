package indexer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/excludes"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
)

// TestIndex_GitignoreDirNegation is the regression test for issue #113:
// a WordPress repo blanket-ignores wp-content/plugins/* and re-includes
// its custom plugins with "!wp-content/plugins/<name>/". go-gitignore's
// "*" matches across "/", so the parent directory wp-content/plugins is
// itself reported as excluded; the walk used to prune it with SkipDir
// before ever reaching the re-included children, indexing only the
// repo-root files. The walk must instead descend the parent and let the
// per-file check honour the negation.
func TestIndex_GitignoreDirNegation(t *testing.T) {
	dir := t.TempDir()
	mustWrite := func(rel, content string) {
		p := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		writeFile(t, p, content)
	}
	php := func(fn string) string { return "<?php\nfunction " + fn + "() { return 1; }\n" }

	// Re-included custom plugin — must be indexed, at any depth.
	mustWrite("wp-content/plugins/foo/bar.php", php("fooBar"))
	mustWrite("wp-content/plugins/foo/sub/deep.php", php("fooDeep"))
	// Sibling plugin with no re-include — must stay excluded.
	mustWrite("wp-content/plugins/other/baz.php", php("otherBaz"))

	reg := parser.NewRegistry()
	reg.Register(languages.NewPHPExtractor())
	cfg := config.Default().Index
	cfg.Workers = 2
	// Emulate the layered exclude list ConfigManager hands the indexer
	// when the repo's .gitignore carries the blanket + negation.
	cfg.Exclude = append(append([]string{}, excludes.Builtin...),
		"wp-content/plugins/*",
		"!wp-content/plugins/foo/",
	)

	g := graph.New()
	idx := New(g, reg, cfg, zap.NewNop())
	_, err := idx.Index(dir)
	require.NoError(t, err)

	hasFile := func(suffix string) bool {
		for _, n := range g.AllNodes() {
			if strings.Contains(filepath.ToSlash(n.FilePath), suffix) {
				return true
			}
		}
		return false
	}

	if !hasFile("wp-content/plugins/foo/bar.php") {
		t.Error("re-included plugin wp-content/plugins/foo/bar.php was not indexed")
	}
	if !hasFile("wp-content/plugins/foo/sub/deep.php") {
		t.Error("re-included nested file wp-content/plugins/foo/sub/deep.php was not indexed")
	}
	if hasFile("wp-content/plugins/other/baz.php") {
		t.Error("excluded sibling wp-content/plugins/other/baz.php leaked into the graph")
	}
}
