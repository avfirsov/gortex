package analysis

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/query"
	"pgregory.net/rapid"
)

// Feature: gortex-enhancements, Property 1: Contract violation detection completeness

// --- Generators ---

// genParamType generates a random Go parameter type.
func genParamType() *rapid.Generator[string] {
	return rapid.SampledFrom([]string{
		"string", "int", "bool", "float64", "error",
		"context.Context", "[]byte", "map[string]any",
		"*http.Request", "io.Reader",
	})
}

// genSignature builds a "func(...) ..." string from a list of param types.
func buildSignature(params []string) string {
	if len(params) == 0 {
		return "func()"
	}
	named := make([]string, len(params))
	for i, p := range params {
		named[i] = fmt.Sprintf("p%d %s", i, p)
	}
	return fmt.Sprintf("func(%s)", strings.Join(named, ", "))
}

// callerGraphResult holds a generated graph with known callers for a target function.
type callerGraphResult struct {
	Graph     *graph.Graph
	Engine    *query.Engine
	TargetID  string
	OldSig    string
	OldParams []string
	CallerIDs []string
}

// genCallerGraph generates a graph with a target function and N callers.
func genCallerGraph() *rapid.Generator[callerGraphResult] {
	return rapid.Custom(func(t *rapid.T) callerGraphResult {
		g := graph.New()

		// Generate 1-5 old params for the target function
		numOldParams := rapid.IntRange(1, 5).Draw(t, "numOldParams")
		oldParams := make([]string, numOldParams)
		for i := range numOldParams {
			oldParams[i] = genParamType().Draw(t, fmt.Sprintf("oldParam%d", i))
		}
		oldSig := buildSignature(oldParams)

		targetID := "pkg/target.go::TargetFunc"
		g.AddNode(&graph.Node{
			ID:        targetID,
			Kind:      graph.KindFunction,
			Name:      "TargetFunc",
			FilePath:  "pkg/target.go",
			StartLine: 10,
			EndLine:   30,
			Language:  "go",
			Meta:      map[string]any{"signature": oldSig},
		})

		// Generate 1-8 callers
		numCallers := rapid.IntRange(1, 8).Draw(t, "numCallers")
		callerIDs := make([]string, numCallers)
		for i := range numCallers {
			callerID := fmt.Sprintf("pkg/caller%d.go::Caller%d", i, i)
			callerIDs[i] = callerID
			g.AddNode(&graph.Node{
				ID:        callerID,
				Kind:      graph.KindFunction,
				Name:      fmt.Sprintf("Caller%d", i),
				FilePath:  fmt.Sprintf("pkg/caller%d.go", i),
				StartLine: 1,
				EndLine:   20,
				Language:  "go",
			})
			g.AddEdge(&graph.Edge{
				From: callerID,
				To:   targetID,
				Kind: graph.EdgeCalls,
			})
		}

		engine := query.NewEngine(g)
		return callerGraphResult{
			Graph:     g,
			Engine:    engine,
			TargetID:  targetID,
			OldSig:    oldSig,
			OldParams: oldParams,
			CallerIDs: callerIDs,
		}
	})
}

// interfaceGraphResult holds a generated graph with interface implementations.
type interfaceGraphResult struct {
	Graph         *graph.Graph
	Engine        *query.Engine
	MethodID      string
	InterfaceID   string
	ParentTypeID  string
	OldSig        string
	OldParams     []string
	ImplTypeIDs   []string
	ImplMethodIDs []string
}

// genInterfaceGraph generates a graph with an interface, a type implementing it,
// and additional implementor types whose methods should be flagged on signature change.
func genInterfaceGraph() *rapid.Generator[interfaceGraphResult] {
	return rapid.Custom(func(t *rapid.T) interfaceGraphResult {
		g := graph.New()

		// Generate 1-4 old params
		numOldParams := rapid.IntRange(1, 4).Draw(t, "numOldParams")
		oldParams := make([]string, numOldParams)
		for i := range numOldParams {
			oldParams[i] = genParamType().Draw(t, fmt.Sprintf("oldParam%d", i))
		}
		oldSig := buildSignature(oldParams)

		// Interface
		interfaceID := "pkg/iface.go::MyInterface"
		g.AddNode(&graph.Node{
			ID:       interfaceID,
			Kind:     graph.KindInterface,
			Name:     "MyInterface",
			FilePath: "pkg/iface.go",
			Language: "go",
		})

		// Primary type that implements the interface
		parentTypeID := "pkg/impl.go::MyType"
		g.AddNode(&graph.Node{
			ID:       parentTypeID,
			Kind:     graph.KindType,
			Name:     "MyType",
			FilePath: "pkg/impl.go",
			Language: "go",
		})
		g.AddEdge(&graph.Edge{
			From: parentTypeID,
			To:   interfaceID,
			Kind: graph.EdgeImplements,
		})

		// Method on the primary type (this is the one being changed)
		methodID := "pkg/impl.go::MyType.DoWork"
		g.AddNode(&graph.Node{
			ID:        methodID,
			Kind:      graph.KindMethod,
			Name:      "DoWork",
			FilePath:  "pkg/impl.go",
			StartLine: 10,
			EndLine:   20,
			Language:  "go",
			Meta:      map[string]any{"signature": oldSig},
		})
		g.AddEdge(&graph.Edge{
			From: methodID,
			To:   parentTypeID,
			Kind: graph.EdgeMemberOf,
		})

		// Generate 1-5 additional implementor types
		numImpls := rapid.IntRange(1, 5).Draw(t, "numImpls")
		implTypeIDs := make([]string, numImpls)
		implMethodIDs := make([]string, numImpls)
		for i := range numImpls {
			implTypeID := fmt.Sprintf("pkg/impl%d.go::ImplType%d", i, i)
			implTypeIDs[i] = implTypeID
			g.AddNode(&graph.Node{
				ID:       implTypeID,
				Kind:     graph.KindType,
				Name:     fmt.Sprintf("ImplType%d", i),
				FilePath: fmt.Sprintf("pkg/impl%d.go", i),
				Language: "go",
			})
			g.AddEdge(&graph.Edge{
				From: implTypeID,
				To:   interfaceID,
				Kind: graph.EdgeImplements,
			})

			// Method on this implementor with the OLD signature
			implMethodID := fmt.Sprintf("pkg/impl%d.go::ImplType%d.DoWork", i, i)
			implMethodIDs[i] = implMethodID
			g.AddNode(&graph.Node{
				ID:        implMethodID,
				Kind:      graph.KindMethod,
				Name:      "DoWork",
				FilePath:  fmt.Sprintf("pkg/impl%d.go", i),
				StartLine: 5,
				EndLine:   15,
				Language:  "go",
				Meta:      map[string]any{"signature": oldSig},
			})
			g.AddEdge(&graph.Edge{
				From: implMethodID,
				To:   implTypeID,
				Kind: graph.EdgeMemberOf,
			})
		}

		engine := query.NewEngine(g)
		return interfaceGraphResult{
			Graph:         g,
			Engine:        engine,
			MethodID:      methodID,
			InterfaceID:   interfaceID,
			ParentTypeID:  parentTypeID,
			OldSig:        oldSig,
			OldParams:     oldParams,
			ImplTypeIDs:   implTypeIDs,
			ImplMethodIDs: implMethodIDs,
		}
	})
}

// --- Property Tests ---

// TestPropertyContractViolation_CallerParamCountChange verifies that when the
// parameter count changes, every caller is reported as a violation.
func TestPropertyContractViolation_CallerParamCountChange(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cg := genCallerGraph().Draw(rt, "callerGraph")

		// Generate a new signature with a DIFFERENT param count
		newParamCount := rapid.IntRange(0, 6).Draw(rt, "newParamCount")
		// Ensure it's different from old count
		for newParamCount == len(cg.OldParams) {
			newParamCount = rapid.IntRange(0, 6).Draw(rt, "newParamCountRetry")
		}
		newParams := make([]string, newParamCount)
		for i := range newParamCount {
			newParams[i] = genParamType().Draw(rt, fmt.Sprintf("newParam%d", i))
		}
		newSig := buildSignature(newParams)

		changes := []SignatureChange{{
			SymbolID:     cg.TargetID,
			NewSignature: newSig,
		}}

		result := VerifyChanges(cg.Graph, cg.Engine, changes)

		// Every caller should be reported as a violation
		violatedCallerIDs := make(map[string]bool)
		for _, v := range result.Violations {
			violatedCallerIDs[v.SymbolID] = true
		}

		for _, callerID := range cg.CallerIDs {
			if !violatedCallerIDs[callerID] {
				rt.Errorf("caller %s was not reported as a violation when param count changed from %d to %d",
					callerID, len(cg.OldParams), newParamCount)
			}
		}

		// Should not be clean
		if result.Clean {
			rt.Errorf("result should not be clean when param count changed")
		}

		// CheckedCallers should match actual caller count
		if result.CheckedCallers != len(cg.CallerIDs) {
			rt.Errorf("CheckedCallers = %d, want %d", result.CheckedCallers, len(cg.CallerIDs))
		}
	})
}

// TestPropertyContractViolation_CallerTypeChange verifies that when a parameter
// type changes (but count stays the same), every caller is reported as a violation.
func TestPropertyContractViolation_CallerTypeChange(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cg := genCallerGraph().Draw(rt, "callerGraph")

		// Build new params with same count but at least one different type
		newParams := make([]string, len(cg.OldParams))
		copy(newParams, cg.OldParams)

		// Pick a random index to change
		changeIdx := rapid.IntRange(0, len(cg.OldParams)-1).Draw(rt, "changeIdx")
		newType := genParamType().Draw(rt, "newType")
		// Ensure it's actually different
		for newType == cg.OldParams[changeIdx] {
			newType = genParamType().Draw(rt, "newTypeRetry")
		}
		newParams[changeIdx] = newType
		newSig := buildSignature(newParams)

		changes := []SignatureChange{{
			SymbolID:     cg.TargetID,
			NewSignature: newSig,
		}}

		result := VerifyChanges(cg.Graph, cg.Engine, changes)

		// Every caller should be reported as a violation
		violatedCallerIDs := make(map[string]bool)
		for _, v := range result.Violations {
			violatedCallerIDs[v.SymbolID] = true
		}

		for _, callerID := range cg.CallerIDs {
			if !violatedCallerIDs[callerID] {
				rt.Errorf("caller %s was not reported as a violation when param type changed at index %d",
					callerID, changeIdx)
			}
		}

		if result.Clean {
			rt.Errorf("result should not be clean when param type changed")
		}
	})
}

// TestPropertyContractViolation_NoChange verifies that when the signature
// doesn't change, Clean is true and CheckedCallers matches actual count.
func TestPropertyContractViolation_NoChange(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cg := genCallerGraph().Draw(rt, "callerGraph")

		// Use the same signature — no change
		changes := []SignatureChange{{
			SymbolID:     cg.TargetID,
			NewSignature: cg.OldSig,
		}}

		result := VerifyChanges(cg.Graph, cg.Engine, changes)

		if !result.Clean {
			rt.Errorf("result should be clean when signature is unchanged, got %d violations", len(result.Violations))
			for _, v := range result.Violations {
				rt.Logf("  violation: %s %s: %s", v.SymbolID, v.Kind, v.Description)
			}
		}

		if result.CheckedCallers != len(cg.CallerIDs) {
			rt.Errorf("CheckedCallers = %d, want %d", result.CheckedCallers, len(cg.CallerIDs))
		}
	})
}

// TestPropertyContractViolation_InterfaceParamCountChange verifies that when
// a method's param count changes, all other implementors are flagged.
func TestPropertyContractViolation_InterfaceParamCountChange(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ig := genInterfaceGraph().Draw(rt, "interfaceGraph")

		// Generate a new signature with a DIFFERENT param count
		newParamCount := rapid.IntRange(0, 5).Draw(rt, "newParamCount")
		for newParamCount == len(ig.OldParams) {
			newParamCount = rapid.IntRange(0, 5).Draw(rt, "newParamCountRetry")
		}
		newParams := make([]string, newParamCount)
		for i := range newParamCount {
			newParams[i] = genParamType().Draw(rt, fmt.Sprintf("newParam%d", i))
		}
		newSig := buildSignature(newParams)

		changes := []SignatureChange{{
			SymbolID:     ig.MethodID,
			NewSignature: newSig,
		}}

		result := VerifyChanges(ig.Graph, ig.Engine, changes)

		// Every implementor's method should be reported as an interface_violation
		violatedMethodIDs := make(map[string]bool)
		for _, v := range result.Violations {
			if v.Kind == "interface_violation" {
				violatedMethodIDs[v.SymbolID] = true
			}
		}

		for _, implMethodID := range ig.ImplMethodIDs {
			if !violatedMethodIDs[implMethodID] {
				rt.Errorf("implementor method %s was not reported as interface_violation when param count changed from %d to %d",
					implMethodID, len(ig.OldParams), newParamCount)
			}
		}

		// CheckedImpls should match the number of other implementors
		if result.CheckedImpls != len(ig.ImplTypeIDs) {
			rt.Errorf("CheckedImpls = %d, want %d", result.CheckedImpls, len(ig.ImplTypeIDs))
		}

		if result.Clean {
			rt.Errorf("result should not be clean when interface method param count changed")
		}
	})
}

// TestPropertyContractViolation_InterfaceNoChange verifies that when a method's
// signature doesn't change, no interface violations are reported and CheckedImpls
// matches the actual implementor count.
func TestPropertyContractViolation_InterfaceNoChange(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ig := genInterfaceGraph().Draw(rt, "interfaceGraph")

		// Use the same signature — no change
		changes := []SignatureChange{{
			SymbolID:     ig.MethodID,
			NewSignature: ig.OldSig,
		}}

		result := VerifyChanges(ig.Graph, ig.Engine, changes)

		// No interface violations should be reported
		for _, v := range result.Violations {
			if v.Kind == "interface_violation" {
				rt.Errorf("unexpected interface_violation for %s when signature unchanged: %s",
					v.SymbolID, v.Description)
			}
		}

		if result.CheckedImpls != len(ig.ImplTypeIDs) {
			rt.Errorf("CheckedImpls = %d, want %d", result.CheckedImpls, len(ig.ImplTypeIDs))
		}

		if !result.Clean {
			rt.Errorf("result should be clean when signature is unchanged, got %d violations", len(result.Violations))
		}
	})
}

// --- Unit Test for missing symbol ID ---

func TestVerifyChanges_MissingSymbolContinues(t *testing.T) {
	g := graph.New()

	// Add a real function with a caller
	g.AddNode(&graph.Node{
		ID:        "pkg/real.go::RealFunc",
		Kind:      graph.KindFunction,
		Name:      "RealFunc",
		FilePath:  "pkg/real.go",
		StartLine: 1,
		EndLine:   10,
		Language:  "go",
		Meta:      map[string]any{"signature": "func(x int)"},
	})
	g.AddNode(&graph.Node{
		ID:        "pkg/caller.go::Caller",
		Kind:      graph.KindFunction,
		Name:      "Caller",
		FilePath:  "pkg/caller.go",
		StartLine: 1,
		EndLine:   10,
		Language:  "go",
	})
	g.AddEdge(&graph.Edge{
		From: "pkg/caller.go::Caller",
		To:   "pkg/real.go::RealFunc",
		Kind: graph.EdgeCalls,
	})

	engine := query.NewEngine(g)

	changes := []SignatureChange{
		{SymbolID: "nonexistent::Missing", NewSignature: "func(x int)"},
		{SymbolID: "pkg/real.go::RealFunc", NewSignature: "func(x int, y string)"},
	}

	result := VerifyChanges(g, engine, changes)

	// Should have an error for the missing symbol
	assert.Len(t, result.Errors, 1)
	assert.Contains(t, result.Errors[0], "nonexistent::Missing")

	// Should still detect violations for the real function
	assert.NotEmpty(t, result.Violations)
	assert.False(t, result.Clean)
	assert.Equal(t, 1, result.CheckedCallers)
}
