package graph

import "testing"

type resolverNameScopeFallbackCountingStore struct {
	Store
	nameBatchCalls int
	allNodesCalls  int
}

func (s *resolverNameScopeFallbackCountingStore) FindNodesByNames(names []string) map[string][]*Node {
	s.nameBatchCalls++
	return s.Store.FindNodesByNames(names)
}

func (s *resolverNameScopeFallbackCountingStore) AllNodes() []*Node {
	s.allNodesCalls++
	return s.Store.AllNodes()
}

func TestFindNodesByResolverNameScopesFallbackUsesOneNameBatch(t *testing.T) {
	base := New()
	base.AddBatch([]*Node{
		{ID: "other::go::Shared", Kind: KindFunction, Name: "Shared", RepoPrefix: "other", Language: "go"},
		{ID: "mono::python::Shared", Kind: KindFunction, Name: "Shared", RepoPrefix: "mono", Language: "python"},
		{ID: "mono::go::Shared", Kind: KindFunction, Name: "Shared", RepoPrefix: "mono", Language: "go"},
		{ID: "mono::neutral::Shared", Kind: KindFunction, Name: "Shared", RepoPrefix: "mono", Language: ""},
	}, nil)
	store := &resolverNameScopeFallbackCountingStore{Store: base}
	scopes := []ResolverNameScope{
		{RepoPrefix: "mono", Languages: []string{"", "go"}, Names: []string{"Shared", "Missing"}},
		{RepoPrefix: "mono", Names: []string{"Shared"}},
		{AllRepos: true, Languages: []string{"", "go"}, Names: []string{"Shared"}},
	}
	results, err := FindNodesByResolverNameScopes(store, scopes)
	if err != nil {
		t.Fatal(err)
	}
	if store.nameBatchCalls != 1 || store.allNodesCalls != 0 {
		t.Fatalf("fallback calls = name batch:%d AllNodes:%d, want 1/0", store.nameBatchCalls, store.allNodesCalls)
	}
	assertResolverScopeNodeIDs(t, results[0]["Shared"], []string{"mono::neutral::Shared", "mono::go::Shared"})
	assertResolverScopeNodeIDs(t, results[1]["Shared"], []string{"mono::go::Shared", "mono::neutral::Shared", "mono::python::Shared"})
	assertResolverScopeNodeIDs(t, results[2]["Shared"], []string{"mono::neutral::Shared", "mono::go::Shared", "other::go::Shared"})
}

func assertResolverScopeNodeIDs(t *testing.T, nodes []*Node, want []string) {
	t.Helper()
	if len(nodes) != len(want) {
		t.Fatalf("node count = %d, want %d", len(nodes), len(want))
	}
	for i := range want {
		if nodes[i] == nil || nodes[i].ID != want[i] {
			t.Fatalf("node[%d] = %+v, want %s", i, nodes[i], want[i])
		}
	}
}
