package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

// seedComponentTestGraph builds a hub-and-spoke graph: two SCC
// triangles + one hub every node points at. Gives predictable
// WCC + SCC answers.
func seedComponentTestGraph() *graph.Graph {
	g := graph.New()
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "hub"} {
		g.AddNode(&graph.Node{ID: id, Kind: graph.KindFunction, Name: id, FilePath: id + ".go"})
	}
	edges := [][2]string{
		{"a", "b"}, {"b", "c"}, {"c", "a"}, // triangle 1
		{"d", "e"}, {"e", "f"}, {"f", "d"}, // triangle 2
		{"c", "d"}, // bridge
		{"a", "hub"}, {"b", "hub"}, {"c", "hub"},
		{"d", "hub"}, {"e", "hub"}, {"f", "hub"},
	}
	for _, e := range edges {
		g.AddEdge(&graph.Edge{From: e[0], To: e[1], Kind: graph.EdgeCalls, FilePath: "x.go"})
	}
	return g
}

func TestComputeWCC_OneComponent(t *testing.T) {
	g := seedComponentTestGraph()
	res := ComputeWCC(g, ComponentOptions{})
	require.Len(t, res, 1, "all 7 nodes form one WCC; got %v", res)
	assert.Equal(t, 7, res[0].Size)
}

func TestComputeWCC_HonoursEdgeFilter(t *testing.T) {
	g := seedComponentTestGraph()
	// Filter out the call edges entirely → no surviving edges → every node
	// becomes its own singleton component.
	res := ComputeWCC(g, ComponentOptions{
		EdgeKinds: []graph.EdgeKind{graph.EdgeReferences},
	})
	assert.Len(t, res, 7,
		"with no surviving edges every node should be a singleton; got %v", res)
}

func TestComputeSCC_ThreeComponents(t *testing.T) {
	g := seedComponentTestGraph()
	res := ComputeSCC(g, ComponentOptions{})
	// 7 SCCs: {a,b,c}, {d,e,f}, {hub} (singleton). But the hub is
	// trivial — without MinSize, expect 3 with sizes [3, 3, 1].
	require.GreaterOrEqual(t, len(res), 3)

	bySize := map[int]int{}
	for _, r := range res {
		bySize[r.Size]++
	}
	assert.Equal(t, 2, bySize[3], "should find two 3-node SCCs (the triangles); got %v", res)
}

func TestComputeSCC_MinSize_DropsSingletons(t *testing.T) {
	g := seedComponentTestGraph()
	res := ComputeSCC(g, ComponentOptions{MinSize: 2})
	for _, r := range res {
		assert.GreaterOrEqual(t, r.Size, 2,
			"MinSize=2 should drop singleton SCCs; got %v", r)
	}
}

// TestComputeSCC_Iterative_NoStackOverflow constructs a deep
// straight-line graph (1 -> 2 -> 3 -> ... -> N) to make sure the
// iterative Tarjan stays in heap and doesn't blow the goroutine
// call stack. N = 10k; recursive Tarjan would fall over.
func TestComputeSCC_Iterative_NoStackOverflow(t *testing.T) {
	const n = 10000
	g := graph.New()
	for i := 0; i < n; i++ {
		id := charID(i)
		g.AddNode(&graph.Node{ID: id, Kind: graph.KindFunction, Name: id, FilePath: "x.go"})
	}
	for i := 0; i < n-1; i++ {
		g.AddEdge(&graph.Edge{
			From: charID(i), To: charID(i + 1), Kind: graph.EdgeCalls, FilePath: "x.go",
		})
	}
	res := ComputeSCC(g, ComponentOptions{})
	// A DAG of N nodes has N singleton SCCs.
	assert.Equal(t, n, len(res))
}

func charID(i int) string {
	// fmt.Sprintf is fine but we want zero allocs in the loop body — just
	// build a deterministic string ID.
	const hex = "0123456789abcdef"
	out := make([]byte, 0, 8)
	for x := i; ; x /= 16 {
		out = append([]byte{hex[x%16]}, out...)
		if x < 16 {
			break
		}
	}
	return "n_" + string(out)
}
