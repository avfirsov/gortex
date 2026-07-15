package mcp

import (
	"fmt"
	"testing"
	"time"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/query"
)

func exploreCausalTestNode(id, name, path string) *graph.Node {
	return &graph.Node{
		ID: id, Name: name, QualName: name, Kind: graph.KindFunction,
		FilePath: path, Language: "go", WorkspaceID: "workspace",
		ProjectID: "project", RepoPrefix: "repo",
	}
}

func exploreCausalTestScope() query.QueryOptions {
	return query.QueryOptions{
		WorkspaceID: "workspace",
		ProjectID:   "project",
		RepoAllow:   map[string]bool{"repo": true},
	}
}

func causalHopMap(neighbors []exploreCausalNeighbor) map[string]int {
	result := make(map[string]int, len(neighbors))
	for _, neighbor := range neighbors {
		result[neighbor.node.ID] = neighbor.hop
	}
	return result
}

func TestMinimumExploreCausalHopsHandlesChainAndMatchesEdges(t *testing.T) {
	seed := exploreCausalTestNode("seed", "process_request", "entry.go")
	a := exploreCausalTestNode("a", "validate_request", "validate.go")
	b := exploreCausalTestNode("b", "resolve_policy", "policy.go")
	c := exploreCausalTestNode("c", "apply_policy", "apply.go")
	d := exploreCausalTestNode("d", "persist_result", "store.go")
	sg := &query.SubGraph{
		Nodes: []*graph.Node{seed, a, b, c, d},
		Edges: []*graph.Edge{
			{From: seed.ID, To: a.ID, Kind: graph.EdgeCalls},
			{From: a.ID, To: b.ID, Kind: graph.EdgeMatches},
			{From: b.ID, To: c.ID, Kind: graph.EdgeCalls},
			{From: c.ID, To: d.ID, Kind: graph.EdgeCalls},
		},
	}

	got := causalHopMap(minimumExploreCausalHops(seed.ID, sg, exploreCausalTestScope(), 4, 15))
	want := map[string]int{a.ID: 1, b.ID: 2, c.ID: 3, d.ID: 4}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for id, hop := range want {
		if got[id] != hop {
			t.Fatalf("hop[%s]=%d, want %d (all=%v)", id, got[id], hop, got)
		}
	}
}

func TestMinimumExploreCausalHopsUsesMinimumAcrossDiamondAndCycle(t *testing.T) {
	seed := exploreCausalTestNode("seed", "process_request", "entry.go")
	a := exploreCausalTestNode("a", "route_primary", "a.go")
	b := exploreCausalTestNode("b", "route_fallback", "b.go")
	c := exploreCausalTestNode("c", "shared_handler", "c.go")
	d := exploreCausalTestNode("d", "write_result", "d.go")
	sg := &query.SubGraph{
		Nodes: []*graph.Node{seed, a, b, c, d},
		Edges: []*graph.Edge{
			{From: seed.ID, To: a.ID, Kind: graph.EdgeCalls},
			{From: seed.ID, To: b.ID, Kind: graph.EdgeCalls},
			{From: a.ID, To: c.ID, Kind: graph.EdgeCalls},
			{From: b.ID, To: c.ID, Kind: graph.EdgeCalls},
			{From: c.ID, To: a.ID, Kind: graph.EdgeCalls},
			{From: c.ID, To: d.ID, Kind: graph.EdgeCalls},
		},
	}

	got := causalHopMap(minimumExploreCausalHops(seed.ID, sg, exploreCausalTestScope(), 4, 15))
	want := map[string]int{a.ID: 1, b.ID: 1, c.ID: 2, d.ID: 3}
	if len(got) != len(want) {
		t.Fatalf("cycle or diamond duplicated a node: got %v, want %v", got, want)
	}
	for id, hop := range want {
		if got[id] != hop {
			t.Fatalf("hop[%s]=%d, want %d (all=%v)", id, got[id], hop, got)
		}
	}
}

func TestMinimumExploreCausalHopsBoundsFanoutDeterministically(t *testing.T) {
	seed := exploreCausalTestNode("seed", "process_request", "entry.go")
	sg := &query.SubGraph{Nodes: []*graph.Node{seed}}
	for i := 23; i >= 0; i-- {
		id := fmt.Sprintf("node-%02d", i)
		n := exploreCausalTestNode(id, fmt.Sprintf("handle_branch_%02d", i), "branches.go")
		sg.Nodes = append(sg.Nodes, n)
		sg.Edges = append(sg.Edges, &graph.Edge{From: seed.ID, To: id, Kind: graph.EdgeCalls})
	}

	got := minimumExploreCausalHops(seed.ID, sg, exploreCausalTestScope(), 4, 15)
	if len(got) != 15 {
		t.Fatalf("got %d neighbors, want hard cap 15", len(got))
	}
	for i, neighbor := range got {
		want := fmt.Sprintf("node-%02d", i)
		if neighbor.node.ID != want || neighbor.hop != 1 {
			t.Fatalf("neighbor[%d]=%s hop=%d, want %s hop=1", i, neighbor.node.ID, neighbor.hop, want)
		}
	}
}

func TestMinimumExploreCausalHopsRejectsTestsAndScopeEscapes(t *testing.T) {
	seed := exploreCausalTestNode("seed", "process_request", "entry.go")
	testNode := exploreCausalTestNode("test", "exercise_request", "entry_test.go")
	testNode.Meta = map[string]any{"is_test": true}
	outside := exploreCausalTestNode("outside", "external_adapter", "external.go")
	outside.RepoPrefix = "other"
	insideAfterOutside := exploreCausalTestNode("inside", "persist_result", "store.go")
	sg := &query.SubGraph{
		Nodes: []*graph.Node{seed, testNode, outside, insideAfterOutside},
		Edges: []*graph.Edge{
			{From: seed.ID, To: testNode.ID, Kind: graph.EdgeCalls},
			{From: seed.ID, To: outside.ID, Kind: graph.EdgeCalls},
			{From: outside.ID, To: insideAfterOutside.ID, Kind: graph.EdgeCalls},
		},
	}
	if got := minimumExploreCausalHops(seed.ID, sg, exploreCausalTestScope(), 4, 15); len(got) != 0 {
		t.Fatalf("tests or out-of-scope paths escaped filtering: %+v", got)
	}
}

func TestExploreStrongCausalSeedRejectsWeakAndTestParents(t *testing.T) {
	strong := exploreCausalTestNode("strong", "process_request", "entry.go")
	strongTarget := exploreTarget{node: strong, source: "func process_request() { validate request policy }"}
	if !exploreStrongCausalSeed("request policy processing", strongTarget) {
		t.Fatalf("multi-term callable production seed should be strong")
	}

	weak := exploreCausalTestNode("weak", "run", "entry.go")
	if exploreStrongCausalSeed("request policy processing", exploreTarget{node: weak}) {
		t.Fatalf("generic one-segment parent must not trigger deep traversal")
	}
	strong.Meta = map[string]any{"is_test": true}
	if exploreStrongCausalSeed("request policy processing", strongTarget) {
		t.Fatalf("test parent must not trigger deep traversal")
	}
}

func TestExploreAdmitCausalSeedEnforcesExplicitLimitAndBudget(t *testing.T) {
	cases := []struct {
		name                      string
		concept, explicit, strong bool
		admitted                  int
		elapsed                   time.Duration
		want                      bool
	}{
		{name: "admit", concept: true, strong: true, want: true},
		{name: "explicit", concept: true, explicit: true, strong: true},
		{name: "non-concept", strong: true},
		{name: "weak", concept: true},
		{name: "seed limit", concept: true, strong: true, admitted: exploreCausalSeedLimit},
		{name: "budget", concept: true, strong: true, elapsed: exploreCausalAdmissionBudget},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exploreAdmitCausalSeed(tc.concept, tc.explicit, tc.strong, tc.admitted, tc.elapsed); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestExploreAnswerDraftPromotesOneAccurateMultiHopCallee(t *testing.T) {
	seed := exploreCausalTestNode("seed", "process_request", "entry.go")
	callee := exploreCausalTestNode("callee", "commit_transaction", "store.go")
	targets := []exploreTarget{{
		node:          seed,
		source:        "func process_request() { validate request and commit transaction }",
		causalCallees: []exploreCausalNeighbor{{node: callee, hop: 3}},
	}}
	task := "A request processing failure occurs while committing a transaction through the service pipeline"
	if !exploreQueryIsConceptTask(task) {
		t.Fatalf("test task must exercise concept localization")
	}
	queryTerms := exploreTerminalTerms(task)
	overlap, longest := exploreDraftTermOverlap(queryTerms, seed)
	bodyOverlap := exploreDraftTermSetOverlap(queryTerms, exploreTerminalTerms(targets[0].source))
	if overlap < 2 && bodyOverlap < 2 && (overlap != 1 || longest < 5) {
		t.Fatalf("seed unexpectedly weak: overlap=%d body=%d longest=%d terms=%v", overlap, bodyOverlap, longest, queryTerms)
	}
	entries := exploreAnswerDraft(task, targets)
	structural := 0
	for _, entry := range entries {
		if !entry.structural {
			continue
		}
		structural++
		if entry.node.ID != callee.ID || entry.structuralHop != 3 || entry.evidence != "3-hop callee of ranked #1" {
			t.Fatalf("inaccurate multi-hop evidence: %+v", entry)
		}
	}
	if structural != 1 {
		t.Fatalf("got %d structural entries, want exactly one", structural)
	}
}

func BenchmarkMinimumExploreCausalHops(b *testing.B) {
	seed := exploreCausalTestNode("seed", "process_request", "entry.go")
	sg := &query.SubGraph{Nodes: []*graph.Node{seed}}
	previous := seed
	for i := 0; i < 15; i++ {
		n := exploreCausalTestNode(fmt.Sprintf("node-%02d", i), fmt.Sprintf("handle_stage_%02d", i), "pipeline.go")
		sg.Nodes = append(sg.Nodes, n)
		sg.Edges = append(sg.Edges, &graph.Edge{From: previous.ID, To: n.ID, Kind: graph.EdgeCalls})
		previous = n
	}
	scope := exploreCausalTestScope()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := minimumExploreCausalHops(seed.ID, sg, scope, 4, 15); len(got) != 4 {
			b.Fatalf("got %d nodes, want 4", len(got))
		}
	}
}
