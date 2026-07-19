package resolver

import (
	"fmt"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

type inferenceBoundStore struct {
	graph.Store
	addBatchCalls          int
	maxAddBatchEdges       int
	getNodeCalls           int
	getOutEdgesCalls       int
	getOutEdgesBatchCalls  int
	maxGetOutEdgesBatchIDs int
}

func (s *inferenceBoundStore) AddBatch(nodes []*graph.Node, edges []*graph.Edge) {
	if len(edges) > 0 {
		s.addBatchCalls++
		if len(edges) > s.maxAddBatchEdges {
			s.maxAddBatchEdges = len(edges)
		}
	}
	s.Store.AddBatch(nodes, edges)
}

func (s *inferenceBoundStore) GetNode(id string) *graph.Node {
	s.getNodeCalls++
	return s.Store.GetNode(id)
}

func (s *inferenceBoundStore) GetOutEdges(id string) []*graph.Edge {
	s.getOutEdgesCalls++
	return s.Store.GetOutEdges(id)
}

func (s *inferenceBoundStore) GetOutEdgesByNodeIDs(ids []string) map[string][]*graph.Edge {
	s.getOutEdgesBatchCalls++
	if len(ids) > s.maxGetOutEdgesBatchIDs {
		s.maxGetOutEdgesBatchIDs = len(ids)
	}
	return s.Store.GetOutEdgesByNodeIDs(ids)
}

func TestVisitImplementationMatchesAvoidsCrossRepoCartesian(t *testing.T) {
	const repos = 100
	typesByRepo := make(map[string]map[string]*implementationType, repos)
	ifacesByRepo := make(map[string][]implementationInterface, repos)
	for i := 0; i < repos; i++ {
		repo := fmt.Sprintf("repo-%03d", i)
		typeID := repo + "::Type"
		typesByRepo[repo] = map[string]*implementationType{
			typeID: {
				node:    &graph.Node{ID: typeID, RepoPrefix: repo},
				methods: map[string]struct{}{"Run": {}},
			},
		}
		ifacesByRepo[repo] = []implementationInterface{{id: repo + "::Iface", methods: []string{"Run"}}}
	}

	matches := 0
	comparisons := visitImplementationMatches(typesByRepo, ifacesByRepo, nil, nil,
		func(*implementationType, implementationInterface) { matches++ })
	if comparisons != repos || matches != repos {
		t.Fatalf("comparisons/matches = %d/%d, want %d/%d", comparisons, matches, repos, repos)
	}
	if cartesian := repos * repos; comparisons >= cartesian {
		t.Fatalf("comparison count %d grew to cross-repo Cartesian %d", comparisons, cartesian)
	}
}

func TestVisitImplementationMatchesParityWithReference(t *testing.T) {
	typesByRepo := map[string]map[string]*implementationType{
		"a": {
			"a::Both": {node: &graph.Node{ID: "a::Both", RepoPrefix: "a"}, methods: map[string]struct{}{"Read": {}, "Close": {}, "Extra": {}}},
			"a::Read": {node: &graph.Node{ID: "a::Read", RepoPrefix: "a"}, methods: map[string]struct{}{"Read": {}}},
			"a::Self": {node: &graph.Node{ID: "a::Self", RepoPrefix: "a"}, methods: map[string]struct{}{"Read": {}}},
		},
		"b": {
			"b::Both": {node: &graph.Node{ID: "b::Both", RepoPrefix: "b"}, methods: map[string]struct{}{"Read": {}, "Close": {}}},
		},
	}
	ifacesByRepo := map[string][]implementationInterface{
		"a": {
			{id: "a::Reader", methods: []string{"Read"}},
			{id: "a::ReadCloser", methods: []string{"Close", "Read"}},
			{id: "a::Writer", methods: []string{"Write"}},
			{id: "a::Self", methods: []string{"Read"}},
		},
		"b": {
			{id: "b::Closer", methods: []string{"Close"}},
		},
	}

	cases := []struct {
		name        string
		scopeTypes  map[string]bool
		scopeIfaces map[string]bool
	}{
		{name: "full"},
		{name: "scoped", scopeTypes: map[string]bool{"a::Read": true}, scopeIfaces: map[string]bool{"a::ReadCloser": true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := make(map[string]struct{})
			visitImplementationMatches(typesByRepo, ifacesByRepo, tc.scopeTypes, tc.scopeIfaces,
				func(typeInfo *implementationType, iface implementationInterface) {
					got[inferencePairKey(typeInfo.node.ID, iface.id)] = struct{}{}
				})
			want := referenceImplementationMatches(typesByRepo, ifacesByRepo, tc.scopeTypes, tc.scopeIfaces)
			if len(got) != len(want) {
				t.Fatalf("match count = %d, want %d; got=%v want=%v", len(got), len(want), got, want)
			}
			for key := range want {
				if _, ok := got[key]; !ok {
					t.Fatalf("missing reference match %q; got=%v", key, got)
				}
			}
		})
	}
}

func referenceImplementationMatches(
	typesByRepo map[string]map[string]*implementationType,
	ifacesByRepo map[string][]implementationInterface,
	scopeTypes, scopeIfaces map[string]bool,
) map[string]struct{} {
	out := make(map[string]struct{})
	for typeRepo, types := range typesByRepo {
		for typeID, typeInfo := range types {
			for ifaceRepo, ifaces := range ifacesByRepo {
				for _, iface := range ifaces {
					if typeID == iface.id || typeRepo != ifaceRepo {
						continue
					}
					if scopeTypes != nil && !scopeTypes[typeID] && !scopeIfaces[iface.id] {
						continue
					}
					satisfies := true
					for _, method := range iface.methods {
						if _, ok := typeInfo.methods[method]; !ok {
							satisfies = false
							break
						}
					}
					if satisfies {
						out[inferencePairKey(typeID, iface.id)] = struct{}{}
					}
				}
			}
		}
	}
	return out
}

func TestInferImplementsBoundsMutationBatchesAndWarmNoOp(t *testing.T) {
	g := graph.New()
	iface := &graph.Node{
		ID: "repo::Runner", Kind: graph.KindInterface, RepoPrefix: "repo",
		Meta: map[string]any{"methods": []string{"Run"}},
	}
	nodes := []*graph.Node{iface}
	edges := make([]*graph.Edge, 0, 2*inferenceMutationBatchSize+17)
	const extra = 17
	want := 2*inferenceMutationBatchSize + extra
	for i := 0; i < want; i++ {
		typeID := fmt.Sprintf("repo::Type%04d", i)
		methodID := typeID + ".Run"
		nodes = append(nodes,
			&graph.Node{ID: typeID, Kind: graph.KindType, RepoPrefix: "repo", FilePath: fmt.Sprintf("t%04d.go", i), StartLine: i + 1},
			&graph.Node{ID: methodID, Kind: graph.KindMethod, Name: "Run", RepoPrefix: "repo"},
		)
		edges = append(edges, &graph.Edge{From: methodID, To: typeID, Kind: graph.EdgeMemberOf})
	}
	g.AddBatch(nodes, edges)

	store := &inferenceBoundStore{Store: g}
	r := New(store)
	if added := r.InferImplements(); added != want {
		t.Fatalf("added = %d, want %d", added, want)
	}
	wantBatches := (want + inferenceMutationBatchSize - 1) / inferenceMutationBatchSize
	if store.addBatchCalls != wantBatches || store.maxAddBatchEdges > inferenceMutationBatchSize {
		t.Fatalf("write batches = %d max=%d, want %d and <=%d", store.addBatchCalls, store.maxAddBatchEdges, wantBatches, inferenceMutationBatchSize)
	}
	if store.getNodeCalls != 0 {
		t.Fatalf("point node reads = %d, want 0", store.getNodeCalls)
	}
	writes := store.addBatchCalls
	if added := r.InferImplements(); added != 0 {
		t.Fatalf("warm added = %d, want 0", added)
	}
	if store.addBatchCalls != writes {
		t.Fatalf("warm pass wrote %d additional batches", store.addBatchCalls-writes)
	}
}

func TestInferOverridesBoundsReadsWritesAndWarmNoOp(t *testing.T) {
	g := graph.New()
	const want = inferenceMutationBatchSize + 17
	nodes := make([]*graph.Node, 0, want*4)
	edges := make([]*graph.Edge, 0, want*3)
	for i := 0; i < want; i++ {
		parentID := fmt.Sprintf("repo::Parent%04d", i)
		childID := fmt.Sprintf("repo::Child%04d", i)
		parentMethodID := parentID + ".Run"
		childMethodID := childID + ".Run"
		nodes = append(nodes,
			&graph.Node{ID: parentID, Kind: graph.KindType, RepoPrefix: "repo"},
			&graph.Node{ID: childID, Kind: graph.KindType, RepoPrefix: "repo"},
			&graph.Node{ID: parentMethodID, Kind: graph.KindMethod, Name: "Run", RepoPrefix: "repo"},
			&graph.Node{ID: childMethodID, Kind: graph.KindMethod, Name: "Run", RepoPrefix: "repo", FilePath: fmt.Sprintf("c%04d.go", i), StartLine: i + 1},
		)
		edges = append(edges,
			&graph.Edge{From: parentMethodID, To: parentID, Kind: graph.EdgeMemberOf},
			&graph.Edge{From: childMethodID, To: childID, Kind: graph.EdgeMemberOf},
			&graph.Edge{From: childID, To: parentID, Kind: graph.EdgeExtends, Origin: graph.OriginASTResolved},
		)
	}
	g.AddBatch(nodes, edges)

	store := &inferenceBoundStore{Store: g}
	r := New(store)
	if added := r.InferOverrides(); added != want {
		t.Fatalf("added = %d, want %d", added, want)
	}
	wantBatches := (want + inferenceMutationBatchSize - 1) / inferenceMutationBatchSize
	if store.getOutEdgesBatchCalls != wantBatches || store.maxGetOutEdgesBatchIDs > inferenceMutationBatchSize {
		t.Fatalf("adjacency batches = %d max=%d, want %d and <=%d", store.getOutEdgesBatchCalls, store.maxGetOutEdgesBatchIDs, wantBatches, inferenceMutationBatchSize)
	}
	if store.addBatchCalls != wantBatches || store.maxAddBatchEdges > inferenceMutationBatchSize {
		t.Fatalf("write batches = %d max=%d, want %d and <=%d", store.addBatchCalls, store.maxAddBatchEdges, wantBatches, inferenceMutationBatchSize)
	}
	if store.getNodeCalls != 0 || store.getOutEdgesCalls != 0 {
		t.Fatalf("point reads = node:%d out:%d, want 0/0", store.getNodeCalls, store.getOutEdgesCalls)
	}
	writes := store.addBatchCalls
	if added := r.InferOverrides(); added != 0 {
		t.Fatalf("warm added = %d, want 0", added)
	}
	if store.addBatchCalls != writes {
		t.Fatalf("warm pass wrote %d additional batches", store.addBatchCalls-writes)
	}
}
