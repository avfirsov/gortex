package analysis

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// TestLeidenMatchesLouvainOnSmallGraph confirms Leiden produces a
// sensible partition on the same toy graph the existing Louvain
// tests use. We don't require identical output (the algorithms can
// settle on different local maxima) — just that:
//   - communities are non-empty
//   - every multi-member community has cohesion ≥ 0
//   - modularity is ≥ 0 (positive = better-than-random clustering)
func TestLeidenMatchesLouvainOnSmallGraph(t *testing.T) {
	g := buildTestGraph()
	leiden := DetectCommunitiesLeiden(g)
	louvain := DetectCommunities(g)

	if leiden == nil || louvain == nil {
		t.Fatal("nil result")
	}
	if len(leiden.Communities) == 0 {
		t.Fatal("leiden produced no communities")
	}
	if leiden.Modularity < 0 {
		t.Fatalf("leiden modularity went negative: %v", leiden.Modularity)
	}
	for _, c := range leiden.Communities {
		if c.Size < 1 {
			t.Errorf("leiden community %q is empty", c.ID)
		}
		if c.Cohesion < 0 || c.Cohesion > 1 {
			t.Errorf("leiden community %q has out-of-range cohesion %v", c.ID, c.Cohesion)
		}
	}
	t.Logf("leiden: %d communities, modularity %.3f", len(leiden.Communities), leiden.Modularity)
	t.Logf("louvain: %d communities, modularity %.3f", len(louvain.Communities), louvain.Modularity)
}

// TestLeidenReachableNodes guarantees every node ends up in some
// community in the final result — Leiden shouldn't lose anyone.
func TestLeidenReachableNodes(t *testing.T) {
	g := buildTestGraph()
	res := DetectCommunitiesLeiden(g)

	// Count graph-relevant nodes — singletons (no in/out edges) are
	// intentionally dropped from the result's NodeToComm map.
	expected := 0
	for _, n := range g.AllNodes() {
		if n.Kind == graph.KindFile || n.Kind == graph.KindImport {
			continue
		}
		for _, e := range g.AllEdges() {
			if (e.From == n.ID || e.To == n.ID) && edgeWeight(e.Kind) > 0 {
				expected++
				break
			}
		}
	}

	if len(res.NodeToComm) < expected {
		t.Fatalf("leiden lost nodes: NodeToComm has %d entries, expected at least %d graph-relevant nodes", len(res.NodeToComm), expected)
	}
}

// TestLeidenConnectednessGuarantee is the *signature* benefit of
// Leiden over Louvain: every produced community is a connected
// induced subgraph. Build a graph with a structure that's known to
// trip Louvain into producing a disconnected community (rare in
// practice but reproducible), and confirm Leiden doesn't.
//
// The trip-up pattern: a "bridge" node X strongly connected to
// both clusters A and B. Louvain can move X into A's community
// while leaving A's connectivity weakened, so A's remaining
// members lose direct paths to each other in the induced subgraph.
//
// For our purposes we just verify connectedness as a property of
// every community in a synthetic dense graph.
func TestLeidenConnectednessGuarantee(t *testing.T) {
	g := graph.New()
	add := func(id string) { g.AddNode(&graph.Node{ID: id, Name: id, Kind: graph.KindFunction, FilePath: id + ".go"}) }
	call := func(from, to string) { g.AddEdge(&graph.Edge{From: from, To: to, Kind: graph.EdgeCalls}) }
	// Two dense triangles + bridge node
	for _, id := range []string{"a1", "a2", "a3", "b1", "b2", "b3", "x"} {
		add(id)
	}
	call("a1", "a2"); call("a2", "a3"); call("a3", "a1")
	call("b1", "b2"); call("b2", "b3"); call("b3", "b1")
	call("x", "a1"); call("x", "b1")

	res := DetectCommunitiesLeiden(g)

	// For each community, verify every member can reach every other
	// member via intra-community edges only.
	for _, c := range res.Communities {
		if !isConnectedInGraph(g, c.Members) {
			t.Errorf("leiden community %q (%v) is disconnected", c.ID, c.Members)
		}
	}
}

func isConnectedInGraph(g *graph.Graph, members []string) bool {
	if len(members) <= 1 {
		return true
	}
	memberSet := make(map[string]bool, len(members))
	for _, m := range members {
		memberSet[m] = true
	}
	adj := make(map[string]map[string]bool)
	for _, e := range g.AllEdges() {
		if !memberSet[e.From] || !memberSet[e.To] {
			continue
		}
		if adj[e.From] == nil {
			adj[e.From] = make(map[string]bool)
		}
		if adj[e.To] == nil {
			adj[e.To] = make(map[string]bool)
		}
		adj[e.From][e.To] = true
		adj[e.To][e.From] = true
	}
	// BFS from members[0]
	visited := map[string]bool{members[0]: true}
	queue := []string{members[0]}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		for nbr := range adj[node] {
			if !visited[nbr] {
				visited[nbr] = true
				queue = append(queue, nbr)
			}
		}
	}
	return len(visited) == len(members)
}
