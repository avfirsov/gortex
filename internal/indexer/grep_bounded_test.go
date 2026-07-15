package indexer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
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
