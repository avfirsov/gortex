package astquery

import (
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSASTCatalog_HitsSpecTarget asserts the rationale from the
// N54 gap-analysis row: SAST rule library reaches 190+ rules across
// the supported languages. Failing this test means new rules either
// regressed in count or lost their Category tag.
func TestSASTCatalog_HitsSpecTarget(t *testing.T) {
	all := DescribeDetectors()
	require.NotEmpty(t, all)

	var sastCount, hygieneCount, totalCategorised int
	for _, d := range all {
		switch d.Category {
		case CategorySAST:
			sastCount++
			totalCategorised++
		case CategoryHygiene:
			hygieneCount++
			totalCategorised++
		}
	}
	t.Logf("total detectors=%d sast=%d hygiene=%d uncategorised=%d",
		len(all), sastCount, hygieneCount, len(all)-totalCategorised)

	require.GreaterOrEqual(t, sastCount+hygieneCount, 175,
		"sast + hygiene rules should be ≥175 (N54 target was 190+ across all categories including the 16 legacy uncategorised); got sast=%d hygiene=%d", sastCount, hygieneCount)
	require.GreaterOrEqual(t, sastCount, 150,
		"sast-categorised rules should be ≥150 to maintain Bandit parity")
}

// TestSASTCatalog_AllRulesHaveCWE confirms every sast-categorised rule
// carries a CWE identifier. Pure-hygiene rules don't (intentional —
// they're not security findings).
func TestSASTCatalog_AllRulesHaveCWE(t *testing.T) {
	missing := []string{}
	for _, d := range DescribeDetectors() {
		if d.Category != CategorySAST {
			continue
		}
		if strings.TrimSpace(d.CWE) == "" {
			missing = append(missing, d.Name)
		}
	}
	sort.Strings(missing)
	assert.Empty(t, missing, "every SAST rule must carry a CWE id; missing on: %v", missing)
}

// TestSASTCatalog_LanguageCoverage spot-checks that each tracked
// language has at least the floor count of rules the gap-analysis
// row commits to.
func TestSASTCatalog_LanguageCoverage(t *testing.T) {
	want := map[string]int{
		"python":     90,
		"go":         15,
		"javascript": 10,
		"typescript": 10,
		"java":       8,
		"ruby":       8,
		"php":        8,
		"rust":       4,
	}
	counts := map[string]int{}
	for _, d := range DescribeDetectors() {
		if d.Category != CategorySAST && d.Category != CategoryHygiene {
			continue
		}
		for _, lang := range d.Languages {
			counts[lang]++
		}
	}
	t.Logf("per-language counts: %+v", counts)
	for lang, floor := range want {
		assert.GreaterOrEqual(t, counts[lang], floor,
			"language %q has %d rules; floor is %d", lang, counts[lang], floor)
	}
}

// TestSASTCatalog_NoDuplicateNames keeps the registry honest — a
// duplicate Name silently overwrites the older entry via the
// RegisterDetector map.
func TestSASTCatalog_NoDuplicateNames(t *testing.T) {
	seen := make(map[string]int)
	for _, d := range DescribeDetectors() {
		seen[d.Name]++
	}
	for name, n := range seen {
		require.Equal(t, 1, n, "detector name %q registered %d times", name, n)
	}
}

// TestSASTCatalog_AllSeveritiesValid enforces the small severity
// taxonomy so downstream consumers (SARIF / DefectDojo / GitHub
// Code Scanning) don't see surprise labels.
func TestSASTCatalog_AllSeveritiesValid(t *testing.T) {
	allowed := map[string]struct{}{"error": {}, "warning": {}, "info": {}}
	for _, d := range DescribeDetectors() {
		_, ok := allowed[strings.ToLower(d.Severity)]
		assert.True(t, ok, "rule %q has invalid severity %q", d.Name, d.Severity)
	}
}

// TestDetectorsByCategory exercises the public lookup the analyze
// dispatcher relies on. A simple round-trip: every Category we filter
// for must come back, and an unknown category returns 0.
func TestDetectorsByCategory(t *testing.T) {
	sast := DetectorsByCategory(CategorySAST)
	require.NotEmpty(t, sast)
	for _, d := range sast {
		require.Equal(t, CategorySAST, d.Category)
	}

	hyg := DetectorsByCategory(CategoryHygiene)
	require.NotEmpty(t, hyg)
	for _, d := range hyg {
		require.Equal(t, CategoryHygiene, d.Category)
	}

	none := DetectorsByCategory("does-not-exist")
	require.Empty(t, none)

	all := DetectorsByCategory()
	require.GreaterOrEqual(t, len(all), len(sast)+len(hyg))
}

// TestLookupDetector returns nil for unknown names and a *Detector
// for known ones — the contract analyze handlers rely on.
func TestLookupDetector(t *testing.T) {
	require.NotNil(t, LookupDetector("py-eval-use"))
	require.NotNil(t, LookupDetector("go-tls-insecure-skip-verify"))
	require.NotNil(t, LookupDetector("js-eval-use"))
	require.Nil(t, LookupDetector("xyzzy-does-not-exist"))
}
