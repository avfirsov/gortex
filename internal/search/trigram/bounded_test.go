package trigram

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func boundedSearchFixture(t testing.TB, count int, body func(int) string) (string, []string) {
	t.Helper()
	root := t.TempDir()
	paths := make([]string, 0, count)
	for i := 0; i < count; i++ {
		rel := fmt.Sprintf("src/file_%04d.cs", i)
		path := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body(i)), 0o644))
		paths = append(paths, rel)
	}
	return root, paths
}

func TestGrepPathsBoundedCapsShortLiteralFileScans(t *testing.T) {
	root, paths := boundedSearchFixture(t, 10, func(int) string {
		return "Register(\"ku\")\n"
	})

	matches, stats := GrepPathsBounded(context.Background(), root, paths, "ku", 24, 3)

	require.Len(t, matches, 3)
	require.Equal(t, 10, stats.CandidateFiles)
	require.Equal(t, 3, stats.ScannedFiles)
	require.True(t, stats.Incomplete)
}

func TestGrepPathsBoundedHonorsCancellationAndResultCap(t *testing.T) {
	root, paths := boundedSearchFixture(t, 4, func(int) string {
		return "Register(\"shared\")\n"
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	matches, stats := GrepPathsBounded(ctx, root, paths, "shared", 24, 4)
	require.Empty(t, matches)
	require.Zero(t, stats.ScannedFiles)
	require.True(t, stats.Incomplete)

	matches, stats = GrepPathsBounded(context.Background(), root, paths, "shared", 2, 4)
	require.Len(t, matches, 2)
	require.Equal(t, 2, stats.ScannedFiles)
	require.True(t, stats.Incomplete)
}

func TestGrepBoundedUsesWarmTrigramCandidates(t *testing.T) {
	root, paths := boundedSearchFixture(t, 20, func(i int) string {
		if i == 17 {
			return "Register(\"distinctive-locale\")\n"
		}
		return "unrelated content\n"
	})
	searcher := Build(root, paths)

	matches, stats := searcher.GrepBounded(context.Background(), "distinctive-locale", 24, 4)

	require.Len(t, matches, 1)
	require.Equal(t, paths[17], matches[0].Path)
	require.Equal(t, 1, stats.CandidateFiles)
	require.Equal(t, 1, stats.ScannedFiles)
	require.False(t, stats.Incomplete)
}

func BenchmarkGrepPathsBoundedShortLiteralMiss(b *testing.B) {
	root, paths := boundedSearchFixture(b, 512, func(i int) string {
		return fmt.Sprintf("namespace Fixture%04d;\npublic sealed class Type%04d {}\n", i, i)
	})
	ctx := context.Background()
	matches, stats := GrepPathsBounded(ctx, root, paths, "ku", 24, 512)
	require.Empty(b, matches)
	require.False(b, stats.Incomplete)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		GrepPathsBounded(ctx, root, paths, "ku", 24, 512)
	}
}
