package trigram

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestGrepLiteralPathsBoundedSkipsSubstringNoiseAndDeduplicatesFiles(t *testing.T) {
	root, paths := boundedSearchFixture(t, 3, func(i int) string {
		switch i {
		case 0:
			return strings.Repeat("skuValue = 1;\n", 32) +
				"Register(\"ku\");\nRegister(\"ku\");\n"
		case 1:
			return strings.Repeat("kurdish_sku = 1;\n", 32)
		default:
			return "Register(\"ku\");\n"
		}
	})

	matches, stats := GrepLiteralPathsBounded(
		context.Background(), root, paths, "ku", 24, 3, nil,
	)

	require.Len(t, matches, 2)
	require.Equal(t, paths[0], matches[0].Path)
	require.Equal(t, paths[2], matches[1].Path)
	require.NotContains(t, matches[0].Text, "sku")
	require.False(t, stats.Incomplete)
}

func TestGrepLiteralPathsBoundedFindsArbitraryFileBeyondLegacyCap(t *testing.T) {
	const target = 537
	root, paths := boundedSearchFixture(t, 600, func(i int) string {
		if i == target {
			return "Register(\"ku\");\n"
		}
		return "no matching locale\n"
	})

	matches, stats := GrepLiteralPathsBounded(
		context.Background(), root, paths, "ku", 24, 0, func(string) bool { return true },
	)

	require.Len(t, matches, 1)
	require.Equal(t, paths[target], matches[0].Path)
	require.Greater(t, stats.ScannedFiles, 0)
	require.LessOrEqual(t, stats.ScannedFiles, len(paths))
	require.False(t, stats.Incomplete)
}

func TestGrepLiteralPathsBoundedStopsBeforeOpeningFilesWhenCancelled(t *testing.T) {
	paths := make([]string, 900)
	for i := range paths {
		paths[i] = fmt.Sprintf("src/file-%04d.go", i)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	matches, stats := GrepLiteralPathsBounded(
		ctx, t.TempDir(), paths, "ku", 24, 0, func(string) bool { return true },
	)

	require.Empty(t, matches)
	require.Zero(t, stats.ScannedFiles)
	require.True(t, stats.Incomplete)
}

func TestSelectBoundedPathsSpreadsDeterministicallyWithinPreferredClass(t *testing.T) {
	paths := make([]string, 0, 1026)
	for i := 0; i < 1024; i++ {
		paths = append(paths, fmt.Sprintf("src/file-%04d.go", i))
	}
	paths = append(paths, "tests/first_test.go", "tests/second_test.go")
	prefer := func(path string) bool { return strings.HasPrefix(path, "src/") }

	first := selectBoundedPaths(paths, 512, prefer, "ku")
	second := selectBoundedPaths(paths, 512, prefer, "ku")
	otherQuery := selectBoundedPaths(paths, 512, prefer, "fr")

	require.Equal(t, first, second)
	require.NotEqual(t, first, otherQuery)
	require.Len(t, first, 512)
	quarters := [4]int{}
	for _, path := range first {
		require.True(t, prefer(path), "test path displaced a production sample: %s", path)
		var index int
		_, err := fmt.Sscanf(path, "src/file-%04d.go", &index)
		require.NoError(t, err)
		quarters[index/256]++
	}
	require.Equal(t, [4]int{128, 128, 128, 128}, quarters)
}

func TestSelectBoundedPathsFullPermutationKeepsProductionBeforeTests(t *testing.T) {
	paths := make([]string, 0, 602)
	for i := 0; i < 600; i++ {
		paths = append(paths, fmt.Sprintf("src/file-%04d.go", i))
	}
	paths = append(paths, "tests/first_test.go", "tests/second_test.go")
	prefer := func(path string) bool { return strings.HasPrefix(path, "src/") }

	selected := selectBoundedPaths(paths, 0, prefer, "ku")

	require.Len(t, selected, len(paths))
	require.Len(t, uniqueStrings(selected), len(paths))
	for _, path := range selected[:600] {
		require.True(t, prefer(path), "test path preceded production path: %s", path)
	}
	require.False(t, prefer(selected[600]))
	require.False(t, prefer(selected[601]))
}

func uniqueStrings(values []string) map[string]struct{} {
	unique := make(map[string]struct{}, len(values))
	for _, value := range values {
		unique[value] = struct{}{}
	}
	return unique
}

func TestGrepLiteralBoundedWarmSkipsSubstringNoise(t *testing.T) {
	root, paths := boundedSearchFixture(t, 32, func(i int) string {
		if i == 31 {
			return "Register(\"ku\");\n"
		}
		return strings.Repeat("skuValue = 1;\n", 8)
	})
	searcher := Build(root, paths)

	matches, stats := searcher.GrepLiteralBounded(
		context.Background(), "ku", 24, 32, nil,
	)

	require.Len(t, matches, 1)
	require.Equal(t, paths[31], matches[0].Path)
	require.False(t, stats.Incomplete)
}

func BenchmarkGrepLiteralPathsBoundedShortLiteralNoise(b *testing.B) {
	root, paths := boundedSearchFixture(b, 512, func(i int) string {
		if i == 511 {
			return strings.Repeat("skuValue = 1;\n", 8) + "Register(\"ku\");\n"
		}
		return strings.Repeat("skuValue = 1;\n", 8)
	})
	ctx := context.Background()
	matches, stats := GrepLiteralPathsBounded(ctx, root, paths, "ku", 24, 512, nil)
	require.Len(b, matches, 1)
	require.False(b, stats.Incomplete)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		GrepLiteralPathsBounded(ctx, root, paths, "ku", 24, 512, nil)
	}
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
