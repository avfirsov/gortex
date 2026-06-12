package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestParseEdgeKindsCSV(t *testing.T) {
	t.Run("single", func(t *testing.T) {
		got, err := ParseEdgeKindsCSV("calls")
		require.NoError(t, err)
		assert.Equal(t, []graph.EdgeKind{graph.EdgeCalls}, got)
	})

	t.Run("multiple with whitespace", func(t *testing.T) {
		got, err := ParseEdgeKindsCSV(" calls , references ")
		require.NoError(t, err)
		assert.Equal(t, []graph.EdgeKind{graph.EdgeCalls, graph.EdgeReferences}, got)
	})

	t.Run("empty tokens skipped", func(t *testing.T) {
		got, err := ParseEdgeKindsCSV("calls,,implements")
		require.NoError(t, err)
		assert.Equal(t, []graph.EdgeKind{graph.EdgeCalls, graph.EdgeImplements}, got)
	})

	t.Run("dedup", func(t *testing.T) {
		got, err := ParseEdgeKindsCSV("calls,calls,references")
		require.NoError(t, err)
		assert.Equal(t, []graph.EdgeKind{graph.EdgeCalls, graph.EdgeReferences}, got)
	})

	t.Run("empty input is nil no error", func(t *testing.T) {
		got, err := ParseEdgeKindsCSV("   ")
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("case insensitive", func(t *testing.T) {
		got, err := ParseEdgeKindsCSV("CALLS")
		require.NoError(t, err)
		assert.Equal(t, []graph.EdgeKind{graph.EdgeCalls}, got)
	})

	t.Run("unknown kind errors", func(t *testing.T) {
		_, err := ParseEdgeKindsCSV("calls,bogus")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bogus")
	})
}

func TestWalkBudgeted_Direction(t *testing.T) {
	e := NewEngine(buildTestGraph())

	t.Run("out follows callees", func(t *testing.T) {
		sg := e.WalkBudgeted("main.go::main", WalkOptions{
			EdgeKinds: []graph.EdgeKind{graph.EdgeCalls},
			Direction: "out",
		})
		ids := nodeIDs(sg.Nodes)
		assert.Contains(t, ids, "main.go::main")
		assert.Contains(t, ids, "pkg/server.go::Start")
		assert.Contains(t, ids, "pkg/db.go::Connect")
		assert.Contains(t, ids, "pkg/db.go::Ping")
	})

	t.Run("in follows callers", func(t *testing.T) {
		sg := e.WalkBudgeted("pkg/db.go::Ping", WalkOptions{
			EdgeKinds: []graph.EdgeKind{graph.EdgeCalls},
			Direction: "in",
		})
		ids := nodeIDs(sg.Nodes)
		assert.Contains(t, ids, "pkg/db.go::Connect")
		assert.Contains(t, ids, "pkg/server.go::Start")
		assert.Contains(t, ids, "main.go::main")
		// Forward-only callees of Ping must not appear.
		assert.NotContains(t, ids, "pkg/db.go::DBImpl")
	})

	t.Run("both is undirected", func(t *testing.T) {
		sg := e.WalkBudgeted("pkg/server.go::Start", WalkOptions{
			EdgeKinds: []graph.EdgeKind{graph.EdgeCalls},
			Direction: "both",
		})
		ids := nodeIDs(sg.Nodes)
		// Reaches both the caller (main) and the callee chain.
		assert.Contains(t, ids, "main.go::main")
		assert.Contains(t, ids, "pkg/db.go::Connect")
		assert.Contains(t, ids, "pkg/db.go::Ping")
	})

	t.Run("empty direction defaults to out", func(t *testing.T) {
		sg := e.WalkBudgeted("main.go::main", WalkOptions{
			EdgeKinds: []graph.EdgeKind{graph.EdgeCalls},
		})
		ids := nodeIDs(sg.Nodes)
		assert.Contains(t, ids, "pkg/server.go::Start")
	})
}

func TestWalkBudgeted_EdgeKindFiltering(t *testing.T) {
	e := NewEngine(buildTestGraph())

	t.Run("calls only ignores references", func(t *testing.T) {
		sg := e.WalkBudgeted("main.go::main", WalkOptions{
			EdgeKinds: []graph.EdgeKind{graph.EdgeCalls},
			Direction: "out",
		})
		ids := nodeIDs(sg.Nodes)
		// main references DBImpl, but references is not in the kind set.
		assert.NotContains(t, ids, "pkg/db.go::DBImpl")
	})

	t.Run("references reaches DBImpl", func(t *testing.T) {
		sg := e.WalkBudgeted("main.go::main", WalkOptions{
			EdgeKinds: []graph.EdgeKind{graph.EdgeReferences},
			Direction: "out",
		})
		ids := nodeIDs(sg.Nodes)
		assert.Contains(t, ids, "pkg/db.go::DBImpl")
	})

	t.Run("multiple kinds reach both", func(t *testing.T) {
		sg := e.WalkBudgeted("main.go::main", WalkOptions{
			EdgeKinds: []graph.EdgeKind{graph.EdgeCalls, graph.EdgeReferences},
			Direction: "out",
		})
		ids := nodeIDs(sg.Nodes)
		assert.Contains(t, ids, "pkg/server.go::Start")
		assert.Contains(t, ids, "pkg/db.go::DBImpl")
	})

	t.Run("empty kinds follows every edge", func(t *testing.T) {
		sg := e.WalkBudgeted("main.go::main", WalkOptions{
			Direction: "out",
		})
		ids := nodeIDs(sg.Nodes)
		assert.Contains(t, ids, "pkg/server.go::Start")
		assert.Contains(t, ids, "pkg/db.go::DBImpl")
	})
}

func TestWalkBudgeted_DepthCap(t *testing.T) {
	e := NewEngine(buildTestGraph())

	// Depth 1 from main reaches Start but not Connect (depth 2).
	sg := e.WalkBudgeted("main.go::main", WalkOptions{
		EdgeKinds: []graph.EdgeKind{graph.EdgeCalls},
		Direction: "out",
		MaxDepth:  1,
	})
	ids := nodeIDs(sg.Nodes)
	assert.Contains(t, ids, "pkg/server.go::Start")
	assert.NotContains(t, ids, "pkg/db.go::Connect")
	assert.Equal(t, 1, sg.StoppedAtDepth)
	assert.False(t, sg.BudgetHit)
}

func TestWalkBudgeted_TokenBudget(t *testing.T) {
	e := NewEngine(buildTestGraph())

	t.Run("tight budget stops early", func(t *testing.T) {
		// A budget that admits only the seed plus a node or two.
		sg := e.WalkBudgeted("main.go::main", WalkOptions{
			EdgeKinds:   []graph.EdgeKind{graph.EdgeCalls},
			Direction:   "out",
			TokenBudget: 10,
		})
		assert.True(t, sg.BudgetHit)
		assert.True(t, sg.Truncated)
		// The whole 4-node call chain must not have been walked.
		assert.Less(t, len(sg.Nodes), 4)
	})

	t.Run("generous budget completes", func(t *testing.T) {
		sg := e.WalkBudgeted("main.go::main", WalkOptions{
			EdgeKinds:   []graph.EdgeKind{graph.EdgeCalls},
			Direction:   "out",
			TokenBudget: 100000,
		})
		assert.False(t, sg.BudgetHit)
		ids := nodeIDs(sg.Nodes)
		assert.Contains(t, ids, "pkg/db.go::Ping")
	})
}

func TestWalkBudgeted_MissingSeed(t *testing.T) {
	e := NewEngine(buildTestGraph())
	sg := e.WalkBudgeted("does/not::Exist", WalkOptions{
		EdgeKinds: []graph.EdgeKind{graph.EdgeCalls},
	})
	assert.Empty(t, sg.Nodes)
	assert.Empty(t, sg.Edges)
}

func TestWalkBudgeted_WorkspaceScope(t *testing.T) {
	g := buildTestGraph()
	// Tag every node in the test graph with workspace "main", then add
	// a foreign node in workspace "other" reachable by a call edge.
	for _, n := range g.AllNodes() {
		n.WorkspaceID = "main"
	}
	g.AddNode(&graph.Node{
		ID: "other/x.go::Foreign", Kind: graph.KindFunction, Name: "Foreign",
		FilePath: "other/x.go", Language: "go", WorkspaceID: "other",
	})
	g.AddEdge(&graph.Edge{
		From: "pkg/db.go::Ping", To: "other/x.go::Foreign",
		Kind: graph.EdgeCalls, FilePath: "pkg/db.go", Line: 20,
	})
	e := NewEngine(g)

	scoped := e.WalkBudgeted("main.go::main", WalkOptions{
		EdgeKinds:   []graph.EdgeKind{graph.EdgeCalls},
		Direction:   "out",
		WorkspaceID: "main",
	})
	assert.NotContains(t, nodeIDs(scoped.Nodes), "other/x.go::Foreign")

	unscoped := e.WalkBudgeted("main.go::main", WalkOptions{
		EdgeKinds: []graph.EdgeKind{graph.EdgeCalls},
		Direction: "out",
	})
	assert.Contains(t, nodeIDs(unscoped.Nodes), "other/x.go::Foreign")
}
