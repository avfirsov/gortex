package resolver

import (
	"path/filepath"
	"strings"
)

// isTestFilePath reports whether a source path follows a recognised test
// convention.
//
// PURPOSE: let the Temporal orphan detector drop dispatches that
// originate in test fixtures (the dominant broken_dispatch false
// positive) without depending on Node.Meta test flags — which are not
// re-stamped on the incremental-reindex path.
//
// RATIONALE: a verbatim port of internal/indexer.IsTestFile. The
// resolver cannot import the indexer package (indexer → resolver is the
// established import direction; the reverse would be a cycle), so the
// predicate is duplicated here. It is intentionally identical in
// behaviour; the canonical home is internal/indexer/testpattern.go and a
// future refactor should consolidate both (plus the analysis/* copies)
// into a leaf internal/pathutil package.
//
// KEYWORDS: test-file, predicate, temporal, broken_dispatch, no-cycle
func isTestFilePath(path string) bool {
	if path == "" {
		return false
	}
	// Directory-based hints first — covers projects that don't follow
	// the per-file naming convention.
	dir := filepath.ToSlash(path)
	for _, marker := range []string{"/__tests__/", "/tests/", "/test/", "/spec/"} {
		if strings.Contains(dir, marker) {
			return true
		}
	}
	if strings.HasPrefix(dir, "tests/") || strings.HasPrefix(dir, "test/") || strings.HasPrefix(dir, "spec/") {
		return true
	}

	base := filepath.Base(path)
	ext := strings.ToLower(filepath.Ext(base))
	stem := strings.TrimSuffix(base, ext)

	switch ext {
	case ".go":
		// _test.go is the Go convention; _test_<suffix>.go covers test-workflow
		// / test-helper files that dispatch real activities but are not part of
		// production code (the dominant broken_dispatch FP in Temporal corpora).
		// _testworkflow.go (no underscore after "test") is another Temporal test
		// convention — these files define mock workflows that exercise activities.
		return strings.HasSuffix(stem, "_test") || strings.Contains(stem, "_test_") || strings.Contains(stem, "_testworkflow")
	case ".ts", ".tsx", ".js", ".jsx", ".mts", ".cts", ".mjs", ".cjs":
		return strings.HasSuffix(stem, ".test") || strings.HasSuffix(stem, ".spec")
	case ".py":
		return strings.HasPrefix(stem, "test_") || strings.HasSuffix(stem, "_test")
	case ".dart":
		return strings.HasSuffix(stem, "_test")
	case ".rb":
		return strings.HasSuffix(stem, "_spec") || strings.HasSuffix(stem, "_test")
	case ".java", ".kt":
		return strings.HasSuffix(stem, "Test") || strings.HasSuffix(stem, "Tests")
	case ".cs":
		return strings.HasSuffix(stem, "Tests") || strings.HasSuffix(stem, "Test")
	case ".swift":
		return strings.HasSuffix(stem, "Tests")
	case ".php":
		return strings.HasSuffix(stem, "Test") || strings.HasSuffix(stem, "test")
	}
	return false
}
