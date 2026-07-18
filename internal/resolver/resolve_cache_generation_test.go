package resolver

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestResolveAllNodeOnlyInterleaveRefreshesCurrentPageCaches(t *testing.T) {
	t.Setenv("GORTEX_RESOLVE_CHUNK", "1")
	t.Setenv("GORTEX_RESOLVE_CHUNK_SIZE", "1")

	const repo = "repo"
	const language = "typescript"
	paths := []string{"pkg/first.ts", "pkg/late.ts"}
	fileNodes := []*graph.Node{
		{ID: paths[0], Kind: graph.KindFile, Name: paths[0], FilePath: paths[0], RepoPrefix: repo, Language: language},
		{ID: paths[1], Kind: graph.KindFile, Name: paths[1], FilePath: paths[1], RepoPrefix: repo, Language: language},
	}
	callers := []*graph.Node{
		{ID: "repo::first-caller", Kind: graph.KindFunction, Name: "FirstCaller", FilePath: paths[0], RepoPrefix: repo, Language: language},
		{ID: "repo::late-caller", Kind: graph.KindFunction, Name: "LateCaller", FilePath: paths[1], RepoPrefix: repo, Language: language},
	}
	firstTarget := &graph.Node{ID: "repo::first-target", Kind: graph.KindFunction, Name: "FirstTarget", FilePath: paths[0], RepoPrefix: repo, Language: language}
	lateTarget := &graph.Node{ID: "repo::late-target", Kind: graph.KindFunction, Name: "LateTarget", FilePath: paths[1], RepoPrefix: repo, Language: language}
	edges := []*graph.Edge{
		{From: callers[0].ID, To: graph.UnresolvedMarker + firstTarget.Name, Kind: graph.EdgeCalls, FilePath: paths[0], Line: 10},
		{From: callers[1].ID, To: graph.UnresolvedMarker + lateTarget.Name, Kind: graph.EdgeCalls, FilePath: paths[1], Line: 20},
	}

	g := graph.New()
	g.AddBatch([]*graph.Node{fileNodes[0], fileNodes[1], callers[0], callers[1], firstTarget}, edges)
	base := &resolverBatchCountingStore{Store: g}
	store := &resolveInterleavePagingStore{resolverBatchCountingStore: base, pageEdges: edges}
	r := New(store)

	hookCalls := 0
	var scratchBefore, scratchAfter uint64
	var edgeBefore, edgeAfter uint64
	var mutationBefore, mutationAfter uint64
	r.chunkYieldHook = func() {
		hookCalls++
		scratchBefore = r.scratchGeneration
		edgeBefore = g.EdgeMutationRevision()
		mutationBefore = g.MutationRevision()
		g.AddNode(lateTarget)
		scratchAfter = r.scratchGeneration
		edgeAfter = g.EdgeMutationRevision()
		mutationAfter = g.MutationRevision()
	}

	stats := r.ResolveAll()
	if hookCalls != 1 {
		t.Fatalf("chunk yield hook calls=%d, want 1", hookCalls)
	}
	if scratchAfter != scratchBefore {
		t.Fatalf("direct node mutation changed local scratch generation from %d to %d", scratchBefore, scratchAfter)
	}
	if edgeAfter != edgeBefore {
		t.Fatalf("node-only mutation changed edge revision from %d to %d", edgeBefore, edgeAfter)
	}
	if mutationAfter <= mutationBefore {
		t.Fatalf("general mutation revision=%d after %d, want advance", mutationAfter, mutationBefore)
	}
	if stats.PendingBefore != 2 || stats.Resolved != 2 {
		t.Fatalf("stats pending=%d resolved=%d, want 2/2", stats.PendingBefore, stats.Resolved)
	}
	if edges[0].To != firstTarget.ID || edges[1].To != lateTarget.ID {
		t.Fatalf("resolved targets=(%q,%q), want (%q,%q)", edges[0].To, edges[1].To, firstTarget.ID, lateTarget.ID)
	}
	if store.findNodesByNamesCalls != 2 {
		t.Fatalf("name-cache warms=%d, want initial + node-mutation refresh", store.findNodesByNamesCalls)
	}
	if store.getEdgeCandidatesCalls != 0 {
		t.Fatalf("edge liveness calls=%d, want 0 for node-only interleave", store.getEdgeCandidatesCalls)
	}
	if store.edgeExistsCalls != 0 {
		t.Fatalf("point EdgeExists calls=%d, want 0", store.edgeExistsCalls)
	}
}
