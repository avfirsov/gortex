package analysis

import (
	"fmt"
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"pgregory.net/rapid"
)

// Feature: gortex-enhancements, Property 8: Detected cycles are valid cycles

// --- Generators ---

// cycleGraphResult holds a generated graph with known cycles for validation.
type cycleGraphResult struct {
	Graph       *graph.Graph
	Communities *CommunityResult
	Scope       string   // optional scope prefix; empty means no scope
	ScopeNodes  []string // node IDs that are in scope
}

// genCycleGraph generates a random directed graph with known cycles.
// It creates nodes across different file paths and adds edges that form cycles,
// plus some acyclic edges for noise.
func genCycleGraph() *rapid.Generator[cycleGraphResult] {
	return rapid.Custom(func(t *rapid.T) cycleGraphResult {
		g := graph.New()

		// Choose 2-3 package prefixes
		prefixes := []string{"pkg/alpha", "pkg/beta", "pkg/gamma"}
		numPrefixes := rapid.IntRange(2, 3).Draw(t, "numPrefixes")
		usedPrefixes := prefixes[:numPrefixes]

		nodeToComm := make(map[string]string)
		var allIDs []string

		// Create 3-8 function nodes spread across prefixes
		numNodes := rapid.IntRange(3, 8).Draw(t, "numNodes")
		for i := range numNodes {
			prefixIdx := rapid.IntRange(0, numPrefixes-1).Draw(t, fmt.Sprintf("prefix%d", i))
			prefix := usedPrefixes[prefixIdx]
			id := fmt.Sprintf("%s/mod%d.go::func%d", prefix, i, i)
			allIDs = append(allIDs, id)
			nodeToComm[id] = fmt.Sprintf("community-%d", prefixIdx)

			g.AddNode(&graph.Node{
				ID:        id,
				Kind:      graph.KindFunction,
				Name:      fmt.Sprintf("func%d", i),
				FilePath:  fmt.Sprintf("%s/mod%d.go", prefix, i),
				StartLine: 1,
				EndLine:   10,
				Language:  "go",
			})
		}

		// Add 1-3 cycles by creating directed edges that form loops
		numCycles := rapid.IntRange(1, 3).Draw(t, "numCycles")
		for c := 0; c < numCycles; c++ {
			// Pick 2-4 nodes to form a cycle
			cycleLen := rapid.IntRange(2, min(4, numNodes)).Draw(t, fmt.Sprintf("cycleLen%d", c))
			// Pick distinct node indices
			indices := pickDistinct(t, numNodes, cycleLen, fmt.Sprintf("cycle%d", c))

			// Choose edge kind: calls or imports
			useImports := rapid.Bool().Draw(t, fmt.Sprintf("useImports%d", c))
			edgeKind := graph.EdgeCalls
			if useImports {
				edgeKind = graph.EdgeImports
			}

			// Create cycle: indices[0] -> indices[1] -> ... -> indices[n-1] -> indices[0]
			for i := 0; i < cycleLen; i++ {
				from := allIDs[indices[i]]
				to := allIDs[indices[(i+1)%cycleLen]]
				g.AddEdge(&graph.Edge{
					From: from,
					To:   to,
					Kind: edgeKind,
				})
			}
		}

		// Add some acyclic edges for noise
		numAcyclic := rapid.IntRange(0, numNodes).Draw(t, "numAcyclic")
		for i := 0; i < numAcyclic; i++ {
			fromIdx := rapid.IntRange(0, numNodes-1).Draw(t, fmt.Sprintf("acyclicFrom%d", i))
			toIdx := rapid.IntRange(0, numNodes-1).Draw(t, fmt.Sprintf("acyclicTo%d", i))
			if fromIdx == toIdx {
				continue
			}
			edgeKind := graph.EdgeCalls
			if rapid.Bool().Draw(t, fmt.Sprintf("acyclicImport%d", i)) {
				edgeKind = graph.EdgeImports
			}
			g.AddEdge(&graph.Edge{
				From: allIDs[fromIdx],
				To:   allIDs[toIdx],
				Kind: edgeKind,
			})
		}

		communities := &CommunityResult{
			NodeToComm: nodeToComm,
		}

		return cycleGraphResult{
			Graph:       g,
			Communities: communities,
			Scope:       "",
			ScopeNodes:  allIDs,
		}
	})
}

// genScopedCycleGraph generates a graph with nodes in different file paths
// and a scope prefix, to test scope filtering.
func genScopedCycleGraph() *rapid.Generator[cycleGraphResult] {
	return rapid.Custom(func(t *rapid.T) cycleGraphResult {
		g := graph.New()

		inScopePrefix := "pkg/inscope"
		outScopePrefix := "pkg/outscope"

		nodeToComm := make(map[string]string)
		var inScopeIDs []string

		// Create 3-5 in-scope nodes
		numInScope := rapid.IntRange(3, 5).Draw(t, "numInScope")
		for i := range numInScope {
			id := fmt.Sprintf("%s/mod%d.go::inFunc%d", inScopePrefix, i, i)
			inScopeIDs = append(inScopeIDs, id)
			nodeToComm[id] = "community-0"

			g.AddNode(&graph.Node{
				ID:        id,
				Kind:      graph.KindFunction,
				Name:      fmt.Sprintf("inFunc%d", i),
				FilePath:  fmt.Sprintf("%s/mod%d.go", inScopePrefix, i),
				StartLine: 1,
				EndLine:   10,
				Language:  "go",
			})
		}

		// Create 2-4 out-of-scope nodes
		numOutScope := rapid.IntRange(2, 4).Draw(t, "numOutScope")
		var outScopeIDs []string
		for i := range numOutScope {
			id := fmt.Sprintf("%s/mod%d.go::outFunc%d", outScopePrefix, i, i)
			outScopeIDs = append(outScopeIDs, id)
			nodeToComm[id] = "community-1"

			g.AddNode(&graph.Node{
				ID:        id,
				Kind:      graph.KindFunction,
				Name:      fmt.Sprintf("outFunc%d", i),
				FilePath:  fmt.Sprintf("%s/mod%d.go", outScopePrefix, i),
				StartLine: 1,
				EndLine:   10,
				Language:  "go",
			})
		}

		// Add a cycle among in-scope nodes
		for i := 0; i < numInScope; i++ {
			g.AddEdge(&graph.Edge{
				From: inScopeIDs[i],
				To:   inScopeIDs[(i+1)%numInScope],
				Kind: graph.EdgeCalls,
			})
		}

		// Add a cycle among out-of-scope nodes
		for i := 0; i < numOutScope; i++ {
			g.AddEdge(&graph.Edge{
				From: outScopeIDs[i],
				To:   outScopeIDs[(i+1)%numOutScope],
				Kind: graph.EdgeCalls,
			})
		}

		// Optionally add cross-scope edges (these should not appear in scoped results)
		if rapid.Bool().Draw(t, "addCrossScope") {
			g.AddEdge(&graph.Edge{
				From: inScopeIDs[0],
				To:   outScopeIDs[0],
				Kind: graph.EdgeCalls,
			})
		}

		communities := &CommunityResult{
			NodeToComm: nodeToComm,
		}

		return cycleGraphResult{
			Graph:       g,
			Communities: communities,
			Scope:       inScopePrefix,
			ScopeNodes:  inScopeIDs,
		}
	})
}

// pickDistinct picks n distinct indices from [0, max).
func pickDistinct(t *rapid.T, max, n int, label string) []int {
	if n > max {
		n = max
	}
	// Generate a permutation and take first n
	perm := make([]int, max)
	for i := range perm {
		perm[i] = i
	}
	// Fisher-Yates shuffle (partial)
	for i := 0; i < n; i++ {
		j := rapid.IntRange(i, max-1).Draw(t, fmt.Sprintf("%s_idx%d", label, i))
		perm[i], perm[j] = perm[j], perm[i]
	}
	return perm[:n]
}

// --- Property Tests ---

// TestPropertyCycleValidity verifies that every cycle returned by DetectCycles
// is a valid cycle: following the edges in path order from the first symbol
// eventually returns to the first symbol. All consecutive pairs in the path
// must have a directed edge between them in the graph.
func TestPropertyCycleValidity(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		tc := genCycleGraph().Draw(rt, "cycleGraph")

		cycles := DetectCycles(tc.Graph, tc.Communities, tc.Scope)

		// Build adjacency set from the graph's actual edges (calls + imports only)
		edgeSet := buildEdgeSet(tc.Graph)

		for i, cycle := range cycles {
			if len(cycle.Path) < 2 {
				rt.Errorf("cycle %d has fewer than 2 nodes: %v", i, cycle.Path)
				continue
			}

			// Verify each consecutive pair has an edge
			for j := 0; j < len(cycle.Path); j++ {
				from := cycle.Path[j]
				to := cycle.Path[(j+1)%len(cycle.Path)]
				pair := edgePair{from, to}
				if !edgeSet[pair] {
					rt.Errorf("cycle %d: no edge from %s to %s (path index %d -> %d)",
						i, from, to, j, (j+1)%len(cycle.Path))
				}
			}

			// Verify cycle kind is valid
			validKinds := map[string]bool{
				"import-cycle":          true,
				"call-cycle":            true,
				"cross-community-cycle": true,
			}
			if !validKinds[cycle.Kind] {
				rt.Errorf("cycle %d has invalid kind: %q", i, cycle.Kind)
			}

			// Verify severity matches kind
			expectedSeverity := map[string]int{
				"import-cycle":          3,
				"call-cycle":            1,
				"cross-community-cycle": 2,
			}
			if cycle.Severity != expectedSeverity[cycle.Kind] {
				rt.Errorf("cycle %d: kind=%q severity=%d, expected %d",
					i, cycle.Kind, cycle.Severity, expectedSeverity[cycle.Kind])
			}

			// Verify all nodes in the cycle actually exist in the graph
			for _, nodeID := range cycle.Path {
				if tc.Graph.GetNode(nodeID) == nil {
					rt.Errorf("cycle %d contains non-existent node: %s", i, nodeID)
				}
			}
		}
	})
}

// TestPropertyCycleScopeFiltering verifies that when a scope is specified,
// all symbols in every reported cycle have file paths matching the scope prefix.
func TestPropertyCycleScopeFiltering(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		tc := genScopedCycleGraph().Draw(rt, "scopedCycleGraph")

		cycles := DetectCycles(tc.Graph, tc.Communities, tc.Scope)

		for i, cycle := range cycles {
			for _, nodeID := range cycle.Path {
				node := tc.Graph.GetNode(nodeID)
				if node == nil {
					rt.Errorf("cycle %d contains non-existent node: %s", i, nodeID)
					continue
				}
				if !strings.HasPrefix(node.FilePath, tc.Scope) {
					rt.Errorf("cycle %d: node %s has file path %q which does not match scope %q",
						i, nodeID, node.FilePath, tc.Scope)
				}
			}
		}

		// Verify that at least one cycle is found (we always add a cycle among in-scope nodes)
		if len(cycles) == 0 {
			rt.Errorf("expected at least one cycle in scope %q, got none", tc.Scope)
		}
	})
}

// TestPropertyCycleSeverityOrdering verifies that cycles are sorted by severity descending.
func TestPropertyCycleSeverityOrdering(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		tc := genCycleGraph().Draw(rt, "cycleGraph")

		cycles := DetectCycles(tc.Graph, tc.Communities, "")

		for i := 1; i < len(cycles); i++ {
			if cycles[i].Severity > cycles[i-1].Severity {
				rt.Errorf("cycles not sorted by severity descending: cycle %d severity=%d > cycle %d severity=%d",
					i, cycles[i].Severity, i-1, cycles[i-1].Severity)
			}
		}
	})
}

// Feature: gortex-enhancements, Property 9: Would-create-cycle matches reachability

// --- Generators ---

// wouldCycleGraphResult holds a generated graph and a pair of nodes to test.
type wouldCycleGraphResult struct {
	Graph  *graph.Graph
	FromID string
	ToID   string
	// Whether toID can reach fromID via directed paths (calls/imports edges)
	ExpectCycle bool
}

// genWouldCycleGraph generates a random graph and picks a pair of nodes,
// computing the expected reachability via reference DFS.
func genWouldCycleGraph() *rapid.Generator[wouldCycleGraphResult] {
	return rapid.Custom(func(t *rapid.T) wouldCycleGraphResult {
		g := graph.New()

		// Create 4-10 nodes
		numNodes := rapid.IntRange(4, 10).Draw(t, "numNodes")
		ids := make([]string, numNodes)
		for i := range numNodes {
			id := fmt.Sprintf("pkg/mod%d.go::func%d", i, i)
			ids[i] = id
			g.AddNode(&graph.Node{
				ID:        id,
				Kind:      graph.KindFunction,
				Name:      fmt.Sprintf("func%d", i),
				FilePath:  fmt.Sprintf("pkg/mod%d.go", i),
				StartLine: 1,
				EndLine:   10,
				Language:  "go",
			})
		}

		// Add random edges (calls and imports)
		numEdges := rapid.IntRange(2, numNodes*2).Draw(t, "numEdges")
		seen := make(map[string]bool)
		adj := make(map[string][]string) // for reference DFS
		for e := 0; e < numEdges; e++ {
			fromIdx := rapid.IntRange(0, numNodes-1).Draw(t, fmt.Sprintf("from%d", e))
			toIdx := rapid.IntRange(0, numNodes-1).Draw(t, fmt.Sprintf("to%d", e))
			if fromIdx == toIdx {
				continue
			}
			key := fmt.Sprintf("%d->%d", fromIdx, toIdx)
			if seen[key] {
				continue
			}
			seen[key] = true

			edgeKind := graph.EdgeCalls
			if rapid.Bool().Draw(t, fmt.Sprintf("isImport%d", e)) {
				edgeKind = graph.EdgeImports
			}

			g.AddEdge(&graph.Edge{
				From: ids[fromIdx],
				To:   ids[toIdx],
				Kind: edgeKind,
			})
			adj[ids[fromIdx]] = append(adj[ids[fromIdx]], ids[toIdx])
		}

		// Pick two distinct nodes
		fromIdx := rapid.IntRange(0, numNodes-1).Draw(t, "fromIdx")
		toIdx := rapid.IntRange(0, numNodes-2).Draw(t, "toIdx")
		if toIdx >= fromIdx {
			toIdx++
		}
		fromID := ids[fromIdx]
		toID := ids[toIdx]

		// Reference DFS: check if toID can reach fromID
		expectCycle := referenceDFS(adj, toID, fromID)

		return wouldCycleGraphResult{
			Graph:       g,
			FromID:      fromID,
			ToID:        toID,
			ExpectCycle: expectCycle,
		}
	})
}

// referenceDFS performs a simple DFS from start looking for target.
func referenceDFS(adj map[string][]string, start, target string) bool {
	visited := make(map[string]bool)
	var dfs func(node string) bool
	dfs = func(node string) bool {
		if node == target {
			return true
		}
		visited[node] = true
		for _, next := range adj[node] {
			if !visited[next] {
				if dfs(next) {
					return true
				}
			}
		}
		return false
	}
	return dfs(start)
}

// --- Property Tests ---

// TestPropertyWouldCreateCycle_MatchesReachability verifies that
// WouldCreateCycle(from_id, to_id) returns true if and only if there exists
// a directed path from to_id to from_id in the current graph.
func TestPropertyWouldCreateCycle_MatchesReachability(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		tc := genWouldCycleGraph().Draw(rt, "wouldCycleGraph")

		wouldCycle, path := WouldCreateCycle(tc.Graph, tc.FromID, tc.ToID)

		if wouldCycle != tc.ExpectCycle {
			rt.Errorf("WouldCreateCycle(%s, %s) = %v, expected %v (based on reference DFS reachability)",
				tc.FromID, tc.ToID, wouldCycle, tc.ExpectCycle)
		}

		if wouldCycle {
			// Verify the returned path is valid: from toID to fromID
			if len(path) < 2 {
				rt.Errorf("cycle path should have at least 2 nodes, got %d: %v", len(path), path)
				return
			}

			// Path should start at toID and end at fromID
			if path[0] != tc.ToID {
				rt.Errorf("cycle path should start at toID=%s, got %s", tc.ToID, path[0])
			}
			if path[len(path)-1] != tc.FromID {
				rt.Errorf("cycle path should end at fromID=%s, got %s", tc.FromID, path[len(path)-1])
			}

			// Build adjacency for path validation
			edgeSet := buildEdgeSet(tc.Graph)

			// Verify each consecutive pair in the path has an edge
			for i := 0; i < len(path)-1; i++ {
				pair := edgePair{path[i], path[i+1]}
				if !edgeSet[pair] {
					rt.Errorf("path has no edge from %s to %s (index %d -> %d)",
						path[i], path[i+1], i, i+1)
				}
			}
		} else {
			// When no cycle, path should be nil/empty
			if len(path) > 0 {
				rt.Errorf("WouldCreateCycle returned false but path is non-empty: %v", path)
			}
		}
	})
}

// TestPropertyWouldCreateCycle_SelfLoop verifies that WouldCreateCycle(a, a)
// always returns true (adding a->a is trivially a cycle since a reaches a).
func TestPropertyWouldCreateCycle_SelfLoop(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		g := graph.New()

		numNodes := rapid.IntRange(1, 5).Draw(rt, "numNodes")
		ids := make([]string, numNodes)
		for i := range numNodes {
			id := fmt.Sprintf("pkg/mod%d.go::func%d", i, i)
			ids[i] = id
			g.AddNode(&graph.Node{
				ID:        id,
				Kind:      graph.KindFunction,
				Name:      fmt.Sprintf("func%d", i),
				FilePath:  fmt.Sprintf("pkg/mod%d.go", i),
				StartLine: 1,
				EndLine:   10,
				Language:  "go",
			})
		}

		// Pick a random node
		idx := rapid.IntRange(0, numNodes-1).Draw(rt, "selfIdx")
		nodeID := ids[idx]

		// WouldCreateCycle(a, a): DFS from a looking for a.
		// The DFS starts at toID (which is a) and looks for fromID (which is also a).
		// Since start == target, it should immediately find it.
		wouldCycle, _ := WouldCreateCycle(g, nodeID, nodeID)

		if !wouldCycle {
			rt.Errorf("WouldCreateCycle(%s, %s) should be true (self-loop)", nodeID, nodeID)
		}
	})
}

// --- Helpers ---

// buildEdgeSet creates a set of directed edge pairs from the graph's calls and imports edges.
func buildEdgeSet(g *graph.Graph) map[edgePair]bool {
	edges := g.AllEdges()
	set := make(map[edgePair]bool)
	for _, e := range edges {
		if e.Kind == graph.EdgeCalls || e.Kind == graph.EdgeImports {
			set[edgePair{e.From, e.To}] = true
		}
	}
	return set
}
