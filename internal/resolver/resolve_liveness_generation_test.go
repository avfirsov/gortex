package resolver

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestResolveAllMutationRevisionRejectsSameKeyReplacement(t *testing.T) {
	t.Setenv("GORTEX_RESOLVE_CHUNK", "1")
	t.Setenv("GORTEX_RESOLVE_CHUNK_SIZE", "1")

	const repo = "repo"
	const language = "typescript"
	paths := []string{"pkg/first.ts", "pkg/replaced.ts"}
	callers := []*graph.Node{
		{ID: "repo::first-caller", Kind: graph.KindFunction, Name: "FirstCaller", FilePath: paths[0], RepoPrefix: repo, Language: language},
		{ID: "repo::replaced-caller", Kind: graph.KindFunction, Name: "ReplacedCaller", FilePath: paths[1], RepoPrefix: repo, Language: language},
	}
	targets := []*graph.Node{
		{ID: "repo::first-target", Kind: graph.KindFunction, Name: "FirstTarget", FilePath: paths[0], RepoPrefix: repo, Language: language},
		{ID: "repo::replaced-target", Kind: graph.KindFunction, Name: "ReplacedTarget", FilePath: paths[1], RepoPrefix: repo, Language: language},
	}
	original := []*graph.Edge{
		{From: callers[0].ID, To: graph.UnresolvedMarker + targets[0].Name, Kind: graph.EdgeCalls, FilePath: paths[0], Line: 10},
		{From: callers[1].ID, To: graph.UnresolvedMarker + targets[1].Name, Kind: graph.EdgeCalls, FilePath: paths[1], Line: 20, Confidence: 0.25, Meta: map[string]any{"version": "old"}},
	}

	g := graph.New()
	g.AddBatch([]*graph.Node{
		{ID: paths[0], Kind: graph.KindFile, Name: paths[0], FilePath: paths[0], RepoPrefix: repo, Language: language},
		{ID: paths[1], Kind: graph.KindFile, Name: paths[1], FilePath: paths[1], RepoPrefix: repo, Language: language},
		callers[0], callers[1], targets[0], targets[1],
	}, original)
	base := &resolverBatchCountingStore{Store: g}
	store := &resolveInterleavePagingStore{resolverBatchCountingStore: base, pageEdges: original}
	r := New(store)

	var replacement *graph.Edge
	var revisionBefore, revisionAfter uint64
	hookCalls := 0
	r.chunkYieldHook = func() {
		hookCalls++
		revisionBefore = g.EdgeMutationRevision()
		if !g.RemoveEdge(original[1].From, original[1].To, original[1].Kind) {
			t.Fatal("remove original edge during interleave")
		}
		copy := *original[1]
		copy.Confidence = 0.95
		copy.Origin = graph.OriginLSPResolved
		copy.Meta = map[string]any{"version": "replacement"}
		replacement = &copy
		g.AddEdge(replacement)
		revisionAfter = g.EdgeMutationRevision()
	}

	stats := r.ResolveAll()
	if hookCalls != 1 {
		t.Fatalf("chunk yield hook calls=%d, want 1", hookCalls)
	}
	if revisionAfter <= revisionBefore {
		t.Fatalf("edge mutation revision=%d after %d, want advance", revisionAfter, revisionBefore)
	}
	if stats.PendingBefore != 2 {
		t.Fatalf("pending before=%d, want 2", stats.PendingBefore)
	}
	if original[0].To != targets[0].ID {
		t.Fatalf("first edge target=%q, want %q", original[0].To, targets[0].ID)
	}
	if original[1].To != graph.UnresolvedMarker+targets[1].Name {
		t.Fatalf("stale edge was applied to %q", original[1].To)
	}
	out := g.GetOutEdges(callers[1].ID)
	if len(out) != 1 || out[0] != replacement {
		t.Fatalf("replacement adjacency=%#v, want exact replacement", out)
	}
	if replacement.To != graph.UnresolvedMarker+targets[1].Name || replacement.Confidence != 0.95 || replacement.Meta["version"] != "replacement" {
		t.Fatalf("replacement payload mutated: %#v", replacement)
	}
	if store.getEdgeCandidatesCalls != 1 {
		t.Fatalf("batched liveness calls=%d, want one remaining-page validation", store.getEdgeCandidatesCalls)
	}
	if store.edgeExistsCalls != 0 {
		t.Fatalf("point EdgeExists calls=%d, want 0", store.edgeExistsCalls)
	}
}
