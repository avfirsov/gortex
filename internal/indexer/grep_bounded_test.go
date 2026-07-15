package indexer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/search/trigram"
)

func TestGrepTextBoundedColdDoesNotBuildTrigramSearcher(t *testing.T) {
	root := t.TempDir()
	rel := "src/FormatterRegistry.cs"
	path := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("Register(\"ku\")\n"), 0o644))
	idx := &Indexer{rootPath: root, fileMtimes: map[string]int64{rel: 1}}

	matches, incomplete := idx.GrepTextBounded(context.Background(), "ku", 24, 8)

	require.Len(t, matches, 1)
	require.False(t, incomplete)
	idx.trigramMu.Lock()
	require.Nil(t, idx.trigramSearcher, "bounded cold search must not build or retain trigram state")
	idx.trigramMu.Unlock()
}

func TestGrepTextBoundedColdCapsFiles(t *testing.T) {
	root := t.TempDir()
	mtimes := make(map[string]int64)
	for i := 0; i < 6; i++ {
		rel := fmt.Sprintf("src/Registry%02d.cs", i)
		path := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("Register(\"shared\")\n"), 0o644))
		mtimes[rel] = 1
	}
	idx := &Indexer{rootPath: root, fileMtimes: mtimes}

	matches, incomplete := idx.GrepTextBounded(context.Background(), "shared", 24, 2)

	require.Len(t, matches, 2)
	require.True(t, incomplete)
}

func TestGrepLiteralForRepoBoundedStampsPaths(t *testing.T) {
	root := t.TempDir()
	rel := "src/FormatterRegistry.cs"
	path := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("Register(\"ku\")\n"), 0o644))
	idx := &Indexer{rootPath: root, fileMtimes: map[string]int64{rel: 1}}
	multi := &MultiIndexer{indexers: map[string]*Indexer{"humanizer": idx}}

	matches, incomplete, owned := multi.GrepLiteralForRepoBounded(
		context.Background(), "humanizer", "ku", 24, 8,
	)

	require.Len(t, matches, 1)
	require.Equal(t, "humanizer/"+rel, matches[0].Path)
	require.False(t, incomplete)
	require.True(t, owned)
}

func TestGrepLiteralForRepoBoundedUsesSoleIndexerForUnprefixedGraph(t *testing.T) {
	for _, registryKey := range []string{"", "humanizer"} {
		t.Run(registryKey, func(t *testing.T) {
			root := t.TempDir()
			rel := "src/FormatterRegistry.cs"
			path := filepath.Join(root, filepath.FromSlash(rel))
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.WriteFile(path, []byte("Register(\"ku\")\n"), 0o644))
			idx := &Indexer{rootPath: root, fileMtimes: map[string]int64{rel: 1}}
			multi := &MultiIndexer{indexers: map[string]*Indexer{registryKey: idx}}

			matches, incomplete, owned := multi.GrepLiteralForRepoBounded(
				context.Background(), "", "ku", 24, 8,
			)

			require.Len(t, matches, 1)
			require.Equal(t, rel, matches[0].Path, "empty-prefix matches must remain unstamped")
			require.False(t, incomplete)
			require.True(t, owned)
		})
	}
}

func TestGrepLiteralForRepoBoundedRejectsEmptyPrefixWithMultipleIndexers(t *testing.T) {
	root := t.TempDir()
	rel := "src/FormatterRegistry.cs"
	path := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("Register(\"ku\")\n"), 0o644))
	idx := &Indexer{rootPath: root, fileMtimes: map[string]int64{rel: 1}}
	multi := &MultiIndexer{indexers: map[string]*Indexer{
		"":      idx,
		"other": {rootPath: t.TempDir(), fileMtimes: map[string]int64{}},
	}}

	matches, incomplete, owned := multi.GrepLiteralForRepoBounded(
		context.Background(), "", "ku", 24, 8,
	)

	require.Empty(t, matches)
	require.False(t, incomplete)
	require.False(t, owned)
}

func TestGrepLiteralBoundedPrioritizesProductionAndDiversifiesFiles(t *testing.T) {
	root := t.TempDir()
	mtimes := make(map[string]int64)
	paths := make([]string, 0, 31)
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
		mtimes[rel] = 1
		paths = append(paths, rel)
	}
	for i := 0; i < 30; i++ {
		write(
			fmt.Sprintf("src/Humanizer.Tests/Locales/Locale%02dTests.cs", i),
			"Register(\"ku\");\nRegister(\"ku\");\n",
		)
	}
	production := "src/Humanizer/Localisation/FormatterRegistry.cs"
	write(production, "Register(\"ku\");\n")
	sort.Strings(paths)
	require.NotEqual(t, production, paths[0], "fixture must put tests before production lexically")

	for _, warm := range []bool{false, true} {
		name := "cold"
		if warm {
			name = "warm"
		}
		t.Run(name, func(t *testing.T) {
			idx := &Indexer{rootPath: root, fileMtimes: mtimes}
			if warm {
				idx.trigramSearcher = trigram.Build(root, paths)
				idx.trigramGen = idx.indexGen.Load()
			}

			matches, incomplete := idx.GrepLiteralBounded(
				context.Background(), "ku", 24, 24,
			)

			require.Len(t, matches, 24)
			require.Equal(t, production, matches[0].Path)
			require.True(t, incomplete)
			seen := make(map[string]struct{}, len(matches))
			for _, match := range matches {
				_, duplicate := seen[match.Path]
				require.False(t, duplicate, "literal recall must keep at most one hit per file")
				seen[match.Path] = struct{}{}
			}
		})
	}
}
