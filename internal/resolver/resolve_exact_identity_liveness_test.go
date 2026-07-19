package resolver

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

type exactLivenessProbeStore struct {
	graph.Store
	exactCalls      int
	candidateCalls  int
	edgeExistsCalls int
	outEdgesCalls   int
	allEdgesCalls   int
	allNodesCalls   int
	identities      []graph.EdgeIdentity
}

func (store *exactLivenessProbeStore) FindEdgesByIdentities(identities []graph.EdgeIdentity) map[graph.EdgeIdentity]*graph.Edge {
	store.exactCalls++
	store.identities = append(store.identities[:0], identities...)
	return store.Store.(graph.EdgeIdentityBatchFinder).FindEdgesByIdentities(identities)
}

func (store *exactLivenessProbeStore) GetEdgeCandidates(endpoints []graph.EdgeEndpoint, sites []graph.EdgeSite) graph.EdgeCandidateSet {
	store.candidateCalls++
	return store.Store.GetEdgeCandidates(endpoints, sites)
}

func (store *exactLivenessProbeStore) EdgeExists(from, to string, kind graph.EdgeKind, filePath string, line int) bool {
	store.edgeExistsCalls++
	for _, edge := range store.Store.GetOutEdges(from) {
		if edge.To == to && edge.Kind == kind && edge.FilePath == filePath && edge.Line == line {
			return true
		}
	}
	return false
}

func (store *exactLivenessProbeStore) GetOutEdges(nodeID string) []*graph.Edge {
	store.outEdgesCalls++
	return store.Store.GetOutEdges(nodeID)
}

func (store *exactLivenessProbeStore) AllEdges() []*graph.Edge {
	store.allEdgesCalls++
	return store.Store.AllEdges()
}

func (store *exactLivenessProbeStore) AllNodes() []*graph.Node {
	store.allNodesCalls++
	return store.Store.AllNodes()
}

func TestResolveJobLivenessPrefersOneExactBatchAndPreservesOldKind(t *testing.T) {
	base := graph.New()
	live := &graph.Edge{
		From: "repo::caller", To: graph.UnresolvedMarker + "Target",
		Kind: graph.EdgeReads, FilePath: "pkg/a.go", Line: 17,
		Confidence: 0.7, Origin: "parser", Meta: map[string]any{"receiver_type": "Client"},
	}
	otherAtSite := &graph.Edge{
		From: live.From, To: graph.UnresolvedMarker + "Other",
		Kind: live.Kind, FilePath: live.FilePath, Line: live.Line,
	}
	base.AddBatch(nil, []*graph.Edge{live, otherAtSite})
	store := &exactLivenessProbeStore{Store: base}

	preResolution := snapshotPersistedEdge(live)
	resolvedShape := *live
	resolvedShape.To = "repo::Target"
	resolvedShape.Kind = graph.EdgeReferences
	staleShape := resolvedShape
	staleSnapshot := preResolution
	staleSnapshot.to = graph.UnresolvedMarker + "Missing"
	jobs := [][]reindexJob{{
		{
			edge: &resolvedShape, preResolution: preResolution,
			oldTo: live.To, oldKind: graph.EdgeReads,
			newTo: resolvedShape.To, kind: graph.EdgeReferences,
		},
		{
			edge: &staleShape, preResolution: staleSnapshot,
			oldTo: staleSnapshot.to, oldKind: graph.EdgeReads,
			newTo: staleShape.To, kind: graph.EdgeReferences,
		},
	}}

	liveness := loadResolveJobLiveness(store, jobs)
	if store.exactCalls != 1 {
		t.Fatalf("exact batch calls=%d, want 1", store.exactCalls)
	}
	if len(store.identities) != 2 {
		t.Fatalf("exact identity count=%d, want 2", len(store.identities))
	}
	if got := store.identities[0]; got.Kind != graph.EdgeReads || got.To != live.To {
		t.Fatalf("live lookup used resolved shape %#v, want old kind=%q target=%q", got, graph.EdgeReads, live.To)
	}
	assertNoBroadLivenessReads(t, store)
	if !liveness.contains(jobs[0][0]) {
		t.Fatal("live old-shape edge was dropped")
	}
	if liveness.contains(jobs[0][1]) {
		t.Fatal("missing exact edge was retained from a same-site neighbor")
	}

	liveCopy := *live
	missingCopy := liveCopy
	missingCopy.To = graph.UnresolvedMarker + "Missing"
	current := loadEdgeLiveness(store, []*graph.Edge{&liveCopy, &missingCopy})
	if store.exactCalls != 2 {
		t.Fatalf("current-shape exact calls=%d, want 2 total", store.exactCalls)
	}
	assertNoBroadLivenessReads(t, store)
	if !current.containsEdge(&liveCopy) || current.containsEdge(&missingCopy) {
		t.Fatal("current-shape exact liveness diverged from full-key presence")
	}
}

func TestResolveJobExactLivenessRejectsSameKeyPayloadReplacement(t *testing.T) {
	base := graph.New()
	original := testPersistedPayloadEdge()
	base.AddEdge(original)
	job := reindexJob{
		edge: original, preResolution: snapshotPersistedEdge(original),
		oldTo: original.To, oldKind: original.Kind,
		newTo: "repo::Target", kind: original.Kind,
	}

	if !base.RemoveEdge(original.From, original.To, original.Kind) {
		t.Fatal("remove original edge")
	}
	replacement := testPersistedPayloadEdge()
	replacement.Origin = graph.OriginLSPResolved
	replacement.Meta["receiver_type"] = "Replacement"
	base.AddEdge(replacement)

	store := &exactLivenessProbeStore{Store: base}
	liveness := loadResolveJobLiveness(store, [][]reindexJob{{job}})
	if store.exactCalls != 1 {
		t.Fatalf("exact batch calls=%d, want 1", store.exactCalls)
	}
	assertNoBroadLivenessReads(t, store)
	if liveness.contains(job) {
		t.Fatal("same-key replacement with changed persisted payload was accepted")
	}
}

func assertNoBroadLivenessReads(t *testing.T, store *exactLivenessProbeStore) {
	t.Helper()
	if store.candidateCalls != 0 || store.edgeExistsCalls != 0 || store.outEdgesCalls != 0 ||
		store.allEdgesCalls != 0 || store.allNodesCalls != 0 {
		t.Fatalf("broad/point liveness reads: candidates=%d exists=%d out=%d allEdges=%d allNodes=%d",
			store.candidateCalls, store.edgeExistsCalls, store.outEdgesCalls,
			store.allEdgesCalls, store.allNodesCalls)
	}
}
