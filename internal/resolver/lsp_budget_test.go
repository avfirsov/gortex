package resolver

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

type slowLSPBudgetHelper struct {
	delay   time.Duration
	defPath string
	defLine int
	ok      bool
	answers map[string]lspBudgetAnswer

	mu    sync.Mutex
	calls []string
}

type lspBudgetAnswer struct {
	delay   time.Duration
	defPath string
	defLine int
	ok      bool
}

func (h *slowLSPBudgetHelper) SupportsPath(path string) bool { return true }

func (h *slowLSPBudgetHelper) Definition(_ string, _ int, name string) (string, int, bool) {
	h.mu.Lock()
	h.calls = append(h.calls, name)
	h.mu.Unlock()
	if answer, exists := h.answers[name]; exists {
		if answer.delay > 0 {
			time.Sleep(answer.delay)
		}
		return answer.defPath, answer.defLine, answer.ok
	}
	if h.delay > 0 {
		time.Sleep(h.delay)
	}
	return h.defPath, h.defLine, h.ok
}

func (h *slowLSPBudgetHelper) callNames() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.calls...)
}

func lspBudgetGraph(names ...string) (*graph.Graph, []*graph.Edge) {
	g := graph.New()
	g.AddNode(&graph.Node{
		ID: "src/caller.ts", Kind: graph.KindFile, Name: "caller.ts",
		FilePath: "src/caller.ts", Language: "typescript",
	})
	edges := make([]*graph.Edge, 0, len(names))
	for i, name := range names {
		callerID := "src/caller.ts::Caller" + name
		g.AddNode(&graph.Node{
			ID: callerID, Kind: graph.KindFunction, Name: "Caller" + name,
			FilePath: "src/caller.ts", StartLine: i + 1, Language: "typescript",
		})
		edge := &graph.Edge{
			From: callerID, To: "unresolved::" + name, Kind: graph.EdgeCalls,
			FilePath: "src/caller.ts", Line: i + 1,
		}
		g.AddEdge(edge)
		edges = append(edges, edge)
	}
	return g, edges
}

func TestLSPResolvePassBudgetFromEnv(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv(LSPResolvePassBudgetEnv, "")
		assert.Equal(t, 15*time.Second, lspResolvePassBudgetFromEnv())
	})
	t.Run("duration override", func(t *testing.T) {
		t.Setenv(LSPResolvePassBudgetEnv, "75ms")
		assert.Equal(t, 75*time.Millisecond, lspResolvePassBudgetFromEnv())
	})
	for _, value := range []string{"0", "off", "none"} {
		t.Run("unlimited "+value, func(t *testing.T) {
			t.Setenv(LSPResolvePassBudgetEnv, value)
			assert.Zero(t, lspResolvePassBudgetFromEnv())
		})
	}
	t.Run("invalid falls back", func(t *testing.T) {
		t.Setenv(LSPResolvePassBudgetEnv, "not-a-duration")
		assert.Equal(t, defaultLSPResolvePassBudget, lspResolvePassBudgetFromEnv())
	})
}

func TestResolveAllDeferredLSPPassBudgetStopsNewCalls(t *testing.T) {
	g, edges := lspBudgetGraph("Alpha", "Beta", "Gamma", "Delta")
	g.AddNode(&graph.Node{
		ID: "src/target.ts::Target", Kind: graph.KindFunction, Name: "Target",
		FilePath: "src/target.ts", StartLine: 50, Language: "typescript",
	})
	helper := &slowLSPBudgetHelper{
		delay: 20 * time.Millisecond, defPath: "src/target.ts", defLine: 50, ok: true,
	}
	r := New(g)
	r.SetLSPHelper(helper)
	r.SetLSPResolvePassBudget(5 * time.Millisecond)

	started := time.Now()
	stats := r.ResolveAll()
	elapsed := time.Since(started)

	require.True(t, stats.LSPBudgetExhausted)
	assert.Equal(t, len(edges), stats.LSPDeferred)
	assert.Equal(t, 1, stats.LSPAttempted, "only the already-in-flight call may outlive the pass budget")
	assert.Equal(t, 1, stats.LSPResolved)
	assert.Equal(t, len(edges)-1, stats.LSPBudgetSkipped)
	assert.Len(t, helper.callNames(), 1, "skipped edges must not invoke the helper")
	assert.GreaterOrEqual(t, elapsed, helper.delay)
	assert.Less(t, elapsed, 100*time.Millisecond,
		"the cumulative breaker must avoid paying the delay once per deferred edge")

	lspResolved := 0
	for _, edge := range edges {
		if edge.Origin == graph.OriginLSPResolved {
			lspResolved++
			assert.Equal(t, "src/target.ts::Target", edge.To,
				"a helper answer completed before the breaker opened must be retained")
		}
	}
	assert.Equal(t, 1, lspResolved)
}

func TestResolveAllDeferredLSPZeroBudgetIsUnlimited(t *testing.T) {
	g, edges := lspBudgetGraph("Alpha", "Beta", "Gamma")
	g.AddNode(&graph.Node{
		ID: "src/target.ts::Target", Kind: graph.KindFunction, Name: "Target",
		FilePath: "src/target.ts", StartLine: 50, Language: "typescript",
	})
	helper := &slowLSPBudgetHelper{
		defPath: "src/target.ts", defLine: 50, ok: true,
	}
	r := New(g)
	r.SetLSPHelper(helper)
	r.SetLSPResolvePassBudget(0)

	stats := r.ResolveAll()

	assert.False(t, stats.LSPBudgetExhausted)
	assert.Equal(t, len(edges), stats.LSPAttempted)
	assert.Equal(t, len(edges), stats.LSPResolved)
	assert.Zero(t, stats.LSPBudgetSkipped)
	assert.Len(t, helper.callNames(), len(edges))
}

func TestResolveDeferredLSPChecksCancellationBeforeAttempt(t *testing.T) {
	g, edges := lspBudgetGraph("Alpha", "Beta")
	helper := &slowLSPBudgetHelper{ok: false}
	r := New(g)
	r.SetLSPHelper(helper)
	deferred := make([]deferredLSPEdge, 0, len(edges))
	for i, edge := range edges {
		deferred = append(deferred, deferredLSPEdge{edge: edge, target: []string{"Alpha", "Beta"}[i]})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := r.resolveDeferredLSP(ctx, deferred)

	assert.Zero(t, result.attempted)
	assert.Equal(t, len(edges), result.skipped)
	assert.False(t, result.budgetExhausted, "caller cancellation is distinct from budget expiry")
	assert.Empty(t, helper.callNames())
}

func TestResolveAllBudgetSkippedLSPIsNotStampedTerminal(t *testing.T) {
	g, edges := lspBudgetGraph("NoDefinitionA", "NoDefinitionB")
	for _, edge := range edges {
		setEdgeTerminal(edge, terminalReasonNoDefinition)
	}
	helper := &slowLSPBudgetHelper{delay: 20 * time.Millisecond, ok: false}
	r := New(g)
	r.SetLSPHelper(helper)
	r.SetLSPResolvePassBudget(5 * time.Millisecond)
	r.SetStampTerminal(true)

	stats := r.ResolveAll()

	require.True(t, stats.LSPBudgetExhausted)
	require.Equal(t, 1, stats.LSPBudgetSkipped)
	attempted := helper.callNames()
	require.Len(t, attempted, 1)
	for _, edge := range edges {
		if edge.To == "unresolved::"+attempted[0] {
			continue
		}
		assert.False(t, edgeTerminalFlag(edge),
			"budget-skipped LSP work must remain retryable on a later scoped pass")
	}
}

func TestResolveAllDeferredLSPBudgetContinuesAfterSlowSortedHead(t *testing.T) {
	g, _ := lspBudgetGraph("Alpha", "Beta", "Gamma")
	helper := &slowLSPBudgetHelper{delay: 20 * time.Millisecond, ok: false}
	r := New(g)
	r.SetLSPHelper(helper)
	r.SetLSPResolvePassBudget(5 * time.Millisecond)

	first := r.ResolveAll()
	require.True(t, first.LSPBudgetExhausted)
	require.Equal(t, 1, first.LSPAttempted)
	require.Equal(t, []string{"Alpha"}, helper.callNames(),
		"the deterministic source order starts at the sorted head")
	assert.Empty(t, r.lspDeferredRetry,
		"unresolved work stays in the graph pending set instead of retaining edge payloads")

	second := r.ResolveAll()
	require.True(t, second.LSPBudgetExhausted)
	require.Equal(t, 1, second.LSPAttempted)
	assert.Equal(t, []string{"Alpha", "Beta"}, helper.callNames(),
		"the next bounded pass must resume at the first budget-skipped edge")
}

func TestResolveAllRetriesBudgetSkippedHeuristicCorrection(t *testing.T) {
	g, edges := lspBudgetGraph("Alpha", "Beta")
	// The normal cascade confidently binds both calls in the caller file. Beta
	// is deliberately wrong; its LSP answer points at the type-aware target in
	// another file. Because Alpha consumes the first pass budget, preserving
	// Beta requires retry state independent of the unresolved-edge scan.
	g.AddNode(&graph.Node{
		ID: "src/caller.ts::WrongAlpha", Kind: graph.KindFunction, Name: "Alpha",
		FilePath: "src/caller.ts", StartLine: 20, Language: "typescript",
	})
	g.AddNode(&graph.Node{
		ID: "src/caller.ts::WrongBeta", Kind: graph.KindFunction, Name: "Beta",
		FilePath: "src/caller.ts", StartLine: 21, Language: "typescript",
	})
	g.AddNode(&graph.Node{
		ID: "src/correct.ts", Kind: graph.KindFile, Name: "correct.ts",
		FilePath: "src/correct.ts", Language: "typescript",
	})
	g.AddNode(&graph.Node{
		ID: "src/correct.ts::CorrectBeta", Kind: graph.KindFunction, Name: "Beta",
		FilePath: "src/correct.ts", StartLine: 50, Language: "typescript",
	})
	helper := &slowLSPBudgetHelper{answers: map[string]lspBudgetAnswer{
		"Alpha": {delay: 20 * time.Millisecond, ok: false},
		"Beta":  {defPath: "src/correct.ts", defLine: 50, ok: true},
	}}
	r := New(g)
	r.SetLSPHelper(helper)
	r.SetLSPResolvePassBudget(5 * time.Millisecond)

	first := r.ResolveAll()
	require.True(t, first.LSPBudgetExhausted)
	require.Equal(t, []string{"Alpha"}, helper.callNames())
	require.Equal(t, "src/caller.ts::WrongBeta", edges[1].To,
		"the skipped edge should retain its best heuristic answer meanwhile")
	require.NotEmpty(t, r.lspDeferredRetry,
		"a resolved edge cannot rely on the next unresolved-edge scan")

	second := r.ResolveAll()
	require.False(t, second.LSPBudgetExhausted)
	require.Equal(t, 1, second.LSPDeferred)
	require.Equal(t, 1, second.LSPAttempted)
	require.Equal(t, 1, second.LSPResolved)
	assert.Equal(t, []string{"Alpha", "Beta"}, helper.callNames())
	assert.Equal(t, "src/correct.ts::CorrectBeta", edges[1].To)
	assert.Equal(t, graph.OriginLSPResolved, edges[1].Origin)
	assert.Empty(t, r.lspDeferredRetry)
}
