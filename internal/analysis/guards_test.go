package analysis

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"pgregory.net/rapid"
)

// Feature: gortex-enhancements, Property 2: Guard rule evaluation correctness

// --- Generators ---

// genDistinctPrefixPair generates two distinct package prefixes.
func genDistinctPrefixPair() *rapid.Generator[[2]string] {
	return rapid.Custom(func(t *rapid.T) [2]string {
		prefixes := []string{
			"internal/parser",
			"internal/mcp",
			"internal/graph",
			"internal/analysis",
			"internal/query",
			"internal/config",
			"internal/indexer",
			"pkg/api",
			"pkg/auth",
			"cmd/server",
		}
		i := rapid.IntRange(0, len(prefixes)-1).Draw(t, "srcIdx")
		j := rapid.IntRange(0, len(prefixes)-2).Draw(t, "tgtIdx")
		if j >= i {
			j++
		}
		return [2]string{prefixes[i], prefixes[j]}
	})
}

// coChangeTestCase holds a generated scenario for co-change rule testing.
type coChangeTestCase struct {
	Graph      *graph.Graph
	Rule       config.GuardRule
	ChangedIDs []string
	HasSource  bool // whether any changed symbol is in source prefix
	HasTarget  bool // whether any changed symbol is in target prefix
}

// genCoChangeTestCase generates a graph with symbols in two packages and a
// co-change rule, along with a change set that may or may not include both.
func genCoChangeTestCase() *rapid.Generator[coChangeTestCase] {
	return rapid.Custom(func(t *rapid.T) coChangeTestCase {
		g := graph.New()
		pair := genDistinctPrefixPair().Draw(t, "prefixPair")
		srcPrefix := pair[0]
		tgtPrefix := pair[1]

		// Create 1-4 symbols in source prefix
		numSrc := rapid.IntRange(1, 4).Draw(t, "numSrc")
		srcIDs := make([]string, numSrc)
		for i := range numSrc {
			id := fmt.Sprintf("%s/file%d.go::Func%d", srcPrefix, i, i)
			srcIDs[i] = id
			g.AddNode(&graph.Node{
				ID:        id,
				Kind:      graph.KindFunction,
				Name:      fmt.Sprintf("Func%d", i),
				FilePath:  fmt.Sprintf("%s/file%d.go", srcPrefix, i),
				StartLine: 1,
				EndLine:   10,
				Language:  "go",
			})
		}

		// Create 1-4 symbols in target prefix
		numTgt := rapid.IntRange(1, 4).Draw(t, "numTgt")
		tgtIDs := make([]string, numTgt)
		for i := range numTgt {
			id := fmt.Sprintf("%s/file%d.go::Func%d", tgtPrefix, i, i)
			tgtIDs[i] = id
			g.AddNode(&graph.Node{
				ID:        id,
				Kind:      graph.KindFunction,
				Name:      fmt.Sprintf("Func%d", i),
				FilePath:  fmt.Sprintf("%s/file%d.go", tgtPrefix, i),
				StartLine: 1,
				EndLine:   10,
				Language:  "go",
			})
		}

		rule := config.GuardRule{
			Name:    "test-co-change",
			Kind:    "co-change",
			Source:  srcPrefix,
			Target:  tgtPrefix,
			Message: fmt.Sprintf("changes to %s require changes to %s", srcPrefix, tgtPrefix),
		}

		// Decide which symbols to include in the change set
		includeSource := rapid.Bool().Draw(t, "includeSource")
		includeTarget := rapid.Bool().Draw(t, "includeTarget")

		var changedIDs []string
		if includeSource {
			// Pick 1 to all source symbols
			count := rapid.IntRange(1, numSrc).Draw(t, "srcChangeCount")
			changedIDs = append(changedIDs, srcIDs[:count]...)
		}
		if includeTarget {
			count := rapid.IntRange(1, numTgt).Draw(t, "tgtChangeCount")
			changedIDs = append(changedIDs, tgtIDs[:count]...)
		}

		return coChangeTestCase{
			Graph:      g,
			Rule:       rule,
			ChangedIDs: changedIDs,
			HasSource:  includeSource,
			HasTarget:  includeTarget,
		}
	})
}

// boundaryTestCase holds a generated scenario for boundary rule testing.
type boundaryTestCase struct {
	Graph              *graph.Graph
	Rule               config.GuardRule
	ChangedIDs         []string
	ExpectViolation    bool
	CrossBoundaryEdges int // number of call/ref edges from source to target among changed symbols
}

// genBoundaryTestCase generates a graph with symbols in two packages, optional
// cross-boundary call/reference edges, and a boundary rule.
func genBoundaryTestCase() *rapid.Generator[boundaryTestCase] {
	return rapid.Custom(func(t *rapid.T) boundaryTestCase {
		g := graph.New()
		pair := genDistinctPrefixPair().Draw(t, "prefixPair")
		srcPrefix := pair[0]
		tgtPrefix := pair[1]

		// Create 1-3 symbols in source prefix
		numSrc := rapid.IntRange(1, 3).Draw(t, "numSrc")
		srcIDs := make([]string, numSrc)
		for i := range numSrc {
			id := fmt.Sprintf("%s/src%d.go::SrcFunc%d", srcPrefix, i, i)
			srcIDs[i] = id
			g.AddNode(&graph.Node{
				ID:        id,
				Kind:      graph.KindFunction,
				Name:      fmt.Sprintf("SrcFunc%d", i),
				FilePath:  fmt.Sprintf("%s/src%d.go", srcPrefix, i),
				StartLine: 1,
				EndLine:   10,
				Language:  "go",
			})
		}

		// Create 1-3 symbols in target prefix
		numTgt := rapid.IntRange(1, 3).Draw(t, "numTgt")
		tgtIDs := make([]string, numTgt)
		for i := range numTgt {
			id := fmt.Sprintf("%s/tgt%d.go::TgtFunc%d", tgtPrefix, i, i)
			tgtIDs[i] = id
			g.AddNode(&graph.Node{
				ID:        id,
				Kind:      graph.KindFunction,
				Name:      fmt.Sprintf("TgtFunc%d", i),
				FilePath:  fmt.Sprintf("%s/tgt%d.go", tgtPrefix, i),
				StartLine: 1,
				EndLine:   10,
				Language:  "go",
			})
		}

		// Optionally add cross-boundary edges (calls or references)
		addCrossBoundary := rapid.Bool().Draw(t, "addCrossBoundary")
		crossEdgeCount := 0
		if addCrossBoundary {
			numEdges := rapid.IntRange(1, numSrc*numTgt).Draw(t, "numEdges")
			seen := make(map[string]bool)
			for e := 0; e < numEdges; e++ {
				fromIdx := rapid.IntRange(0, numSrc-1).Draw(t, fmt.Sprintf("fromIdx%d", e))
				toIdx := rapid.IntRange(0, numTgt-1).Draw(t, fmt.Sprintf("toIdx%d", e))
				key := fmt.Sprintf("%d->%d", fromIdx, toIdx)
				if seen[key] {
					continue
				}
				seen[key] = true

				edgeKind := graph.EdgeCalls
				if rapid.Bool().Draw(t, fmt.Sprintf("isRef%d", e)) {
					edgeKind = graph.EdgeReferences
				}
				g.AddEdge(&graph.Edge{
					From: srcIDs[fromIdx],
					To:   tgtIDs[toIdx],
					Kind: edgeKind,
				})
				crossEdgeCount++
			}
		}

		// Also add some non-boundary edges (within source) to ensure they don't trigger
		if numSrc > 1 {
			g.AddEdge(&graph.Edge{
				From: srcIDs[0],
				To:   srcIDs[1],
				Kind: graph.EdgeCalls,
			})
		}

		rule := config.GuardRule{
			Name:    "test-boundary",
			Kind:    "boundary",
			Source:  srcPrefix,
			Target:  tgtPrefix,
			Message: fmt.Sprintf("%s must not directly reference %s", srcPrefix, tgtPrefix),
		}

		// Changed symbols: always include all source symbols (they're the ones checked)
		changedIDs := make([]string, len(srcIDs))
		copy(changedIDs, srcIDs)

		return boundaryTestCase{
			Graph:              g,
			Rule:               rule,
			ChangedIDs:         changedIDs,
			ExpectViolation:    crossEdgeCount > 0,
			CrossBoundaryEdges: crossEdgeCount,
		}
	})
}

// --- Property Tests ---

// TestPropertyGuardCoChange verifies that EvaluateGuards triggers a co-change
// violation if and only if the change set contains source-prefix symbols but
// not target-prefix symbols.
func TestPropertyGuardCoChange(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		tc := genCoChangeTestCase().Draw(rt, "testCase")

		violations := EvaluateGuards(tc.Graph, []config.GuardRule{tc.Rule}, tc.ChangedIDs)

		expectViolation := tc.HasSource && !tc.HasTarget

		coChangeViolations := filterByKind(violations, "co-change")

		if expectViolation {
			if len(coChangeViolations) == 0 {
				rt.Errorf("expected co-change violation (source=%v, target=%v) but got none",
					tc.HasSource, tc.HasTarget)
			}
			// Verify the violation references the correct rule
			for _, v := range coChangeViolations {
				if v.RuleName != tc.Rule.Name {
					rt.Errorf("violation rule name = %q, want %q", v.RuleName, tc.Rule.Name)
				}
				if v.Kind != "co-change" {
					rt.Errorf("violation kind = %q, want %q", v.Kind, "co-change")
				}
			}
		} else {
			if len(coChangeViolations) > 0 {
				rt.Errorf("expected no co-change violation (source=%v, target=%v) but got %d: %v",
					tc.HasSource, tc.HasTarget, len(coChangeViolations), coChangeViolations)
			}
		}
	})
}

// TestPropertyGuardBoundary verifies that EvaluateGuards triggers a boundary
// violation if and only if there exist call or reference edges from source-prefix
// symbols to target-prefix symbols among the changed symbols.
func TestPropertyGuardBoundary(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		tc := genBoundaryTestCase().Draw(rt, "testCase")

		violations := EvaluateGuards(tc.Graph, []config.GuardRule{tc.Rule}, tc.ChangedIDs)

		boundaryViolations := filterByKind(violations, "boundary")

		if tc.ExpectViolation {
			if len(boundaryViolations) == 0 {
				rt.Errorf("expected boundary violation (crossEdges=%d) but got none",
					tc.CrossBoundaryEdges)
			}
			// Each violation should reference the correct rule
			for _, v := range boundaryViolations {
				if v.RuleName != tc.Rule.Name {
					rt.Errorf("violation rule name = %q, want %q", v.RuleName, tc.Rule.Name)
				}
				if v.Kind != "boundary" {
					rt.Errorf("violation kind = %q, want %q", v.Kind, "boundary")
				}
				// Description should mention the edge
				if !strings.Contains(v.Description, "calls") && !strings.Contains(v.Description, "references") {
					rt.Errorf("boundary violation description should mention edge kind: %q", v.Description)
				}
			}
			// Number of boundary violations should equal the number of cross-boundary edges
			if len(boundaryViolations) != tc.CrossBoundaryEdges {
				rt.Errorf("boundary violations count = %d, want %d (one per cross-boundary edge)",
					len(boundaryViolations), tc.CrossBoundaryEdges)
			}
		} else {
			if len(boundaryViolations) > 0 {
				rt.Errorf("expected no boundary violation but got %d: %v",
					len(boundaryViolations), boundaryViolations)
			}
		}
	})
}

// TestPropertyGuardMixedRules verifies that EvaluateGuards correctly evaluates
// multiple rules of different kinds in a single call.
func TestPropertyGuardMixedRules(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		g := graph.New()

		// Use fixed prefixes for clarity
		srcPrefix := "internal/parser"
		tgtCoChange := "internal/parser/languages"
		tgtBoundary := "internal/graph"

		// Add source symbols
		numSrc := rapid.IntRange(1, 3).Draw(rt, "numSrc")
		srcIDs := make([]string, numSrc)
		for i := range numSrc {
			id := fmt.Sprintf("%s/p%d.go::Parse%d", srcPrefix, i, i)
			srcIDs[i] = id
			g.AddNode(&graph.Node{
				ID:       id,
				Kind:     graph.KindFunction,
				Name:     fmt.Sprintf("Parse%d", i),
				FilePath: fmt.Sprintf("%s/p%d.go", srcPrefix, i),
				Language: "go",
			})
		}

		// Add co-change target symbols
		numCoTgt := rapid.IntRange(1, 3).Draw(rt, "numCoTgt")
		coTgtIDs := make([]string, numCoTgt)
		for i := range numCoTgt {
			id := fmt.Sprintf("%s/lang%d.go::Extract%d", tgtCoChange, i, i)
			coTgtIDs[i] = id
			g.AddNode(&graph.Node{
				ID:       id,
				Kind:     graph.KindFunction,
				Name:     fmt.Sprintf("Extract%d", i),
				FilePath: fmt.Sprintf("%s/lang%d.go", tgtCoChange, i),
				Language: "go",
			})
		}

		// Add boundary target symbols
		numBndTgt := rapid.IntRange(1, 3).Draw(rt, "numBndTgt")
		bndTgtIDs := make([]string, numBndTgt)
		for i := range numBndTgt {
			id := fmt.Sprintf("%s/g%d.go::GraphOp%d", tgtBoundary, i, i)
			bndTgtIDs[i] = id
			g.AddNode(&graph.Node{
				ID:       id,
				Kind:     graph.KindFunction,
				Name:     fmt.Sprintf("GraphOp%d", i),
				FilePath: fmt.Sprintf("%s/g%d.go", tgtBoundary, i),
				Language: "go",
			})
		}

		// Optionally add boundary-crossing edges
		addBoundaryEdge := rapid.Bool().Draw(rt, "addBoundaryEdge")
		if addBoundaryEdge {
			g.AddEdge(&graph.Edge{
				From: srcIDs[0],
				To:   bndTgtIDs[0],
				Kind: graph.EdgeCalls,
			})
		}

		rules := []config.GuardRule{
			{
				Name:    "parser-tests",
				Kind:    "co-change",
				Source:  srcPrefix,
				Target:  tgtCoChange,
				Message: "Parser changes require language extractor updates",
			},
			{
				Name:    "no-direct-graph",
				Kind:    "boundary",
				Source:  srcPrefix,
				Target:  tgtBoundary,
				Message: "Parser must not directly reference graph",
			},
		}

		// Build change set: always include source, optionally include co-change target
		includeCoTarget := rapid.Bool().Draw(rt, "includeCoTarget")
		changedIDs := make([]string, len(srcIDs))
		copy(changedIDs, srcIDs)
		if includeCoTarget {
			changedIDs = append(changedIDs, coTgtIDs[0])
		}

		violations := EvaluateGuards(g, rules, changedIDs)

		// Check co-change: should fire iff source present but co-change target absent
		coChangeViolations := filterByRuleName(violations, "parser-tests")
		expectCoChange := !includeCoTarget // source is always present
		if expectCoChange {
			if len(coChangeViolations) != 1 {
				rt.Errorf("expected 1 co-change violation, got %d", len(coChangeViolations))
			}
		} else {
			if len(coChangeViolations) != 0 {
				rt.Errorf("expected 0 co-change violations, got %d", len(coChangeViolations))
			}
		}

		// Check boundary: should fire iff cross-boundary edge exists
		boundaryViolations := filterByRuleName(violations, "no-direct-graph")
		if addBoundaryEdge {
			if len(boundaryViolations) != 1 {
				rt.Errorf("expected 1 boundary violation, got %d", len(boundaryViolations))
			}
		} else {
			if len(boundaryViolations) != 0 {
				rt.Errorf("expected 0 boundary violations, got %d", len(boundaryViolations))
			}
		}
	})
}

// --- Unit Tests ---

// TestGuardEmptyRules verifies that when no guard rules are configured,
// EvaluateGuards returns an empty result.
func TestGuardEmptyRules(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{
		ID:       "pkg/main.go::Main",
		Kind:     graph.KindFunction,
		Name:     "Main",
		FilePath: "pkg/main.go",
		Language: "go",
	})

	violations := EvaluateGuards(g, nil, []string{"pkg/main.go::Main"})
	assert.Empty(t, violations, "empty rules should produce no violations")

	violations = EvaluateGuards(g, []config.GuardRule{}, []string{"pkg/main.go::Main"})
	assert.Empty(t, violations, "empty rules slice should produce no violations")
}

// TestGuardEmptyChangedSymbols verifies that when no symbols are changed,
// no violations are reported regardless of rules.
func TestGuardEmptyChangedSymbols(t *testing.T) {
	g := graph.New()
	rules := []config.GuardRule{
		{
			Name:   "test-rule",
			Kind:   "co-change",
			Source: "internal/parser",
			Target: "internal/parser/languages",
		},
	}

	violations := EvaluateGuards(g, rules, nil)
	assert.Empty(t, violations)

	violations = EvaluateGuards(g, rules, []string{})
	assert.Empty(t, violations)
}

// TestGuardUnknownSymbolIDs verifies that unknown symbol IDs are gracefully
// skipped without panicking.
func TestGuardUnknownSymbolIDs(t *testing.T) {
	g := graph.New()
	rules := []config.GuardRule{
		{
			Name:   "test-rule",
			Kind:   "co-change",
			Source: "internal/parser",
			Target: "internal/parser/languages",
		},
	}

	violations := EvaluateGuards(g, rules, []string{"nonexistent::Symbol"})
	assert.Empty(t, violations, "unknown symbol IDs should not cause violations or panics")
}

// --- Helpers ---

func filterByKind(violations []GuardViolation, kind string) []GuardViolation {
	var result []GuardViolation
	for _, v := range violations {
		if v.Kind == kind {
			result = append(result, v)
		}
	}
	return result
}

func filterByRuleName(violations []GuardViolation, name string) []GuardViolation {
	var result []GuardViolation
	for _, v := range violations {
		if v.RuleName == name {
			result = append(result, v)
		}
	}
	return result
}
