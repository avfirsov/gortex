package indexer

import (
	"reflect"
	"testing"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/contracts"
	"github.com/zzet/gortex/internal/graph"
)

type contractBatchStore struct {
	graph.Store
	bindingRows    map[graph.SemanticBindingSite]string
	bindingCalls   int
	bindingBatches [][]graph.SemanticBindingSite
	findCalls      int
	findBatches    [][]string
	nodesByName    map[string][]*graph.Node
}

func (s *contractBatchStore) SemanticBindingTypes(sites []graph.SemanticBindingSite) (map[graph.SemanticBindingSite]string, error) {
	s.bindingCalls++
	s.bindingBatches = append(s.bindingBatches, append([]graph.SemanticBindingSite(nil), sites...))
	out := make(map[graph.SemanticBindingSite]string)
	for _, site := range sites {
		if typeName := s.bindingRows[site]; typeName != "" {
			out[site] = typeName
		}
	}
	return out, nil
}

func (s *contractBatchStore) FindNodesByNames(names []string) map[string][]*graph.Node {
	s.findCalls++
	s.findBatches = append(s.findBatches, append([]string(nil), names...))
	out := make(map[string][]*graph.Node)
	for _, name := range names {
		if nodes := s.nodesByName[name]; len(nodes) > 0 {
			out[name] = nodes
		}
	}
	return out
}

type contractBatchResolver struct {
	rows        map[graph.SemanticBindingSite]string
	batchCalls  int
	batchSizes  []int
	legacyCalls int
}

func (r *contractBatchResolver) LookupTypeAtLine(string, int) (string, bool) {
	r.legacyCalls++
	return "legacy-lookup-must-not-run", true
}

func (r *contractBatchResolver) SemanticBindingTypes(sites []graph.SemanticBindingSite) (map[graph.SemanticBindingSite]string, error) {
	r.batchCalls++
	r.batchSizes = append(r.batchSizes, len(sites))
	out := make(map[graph.SemanticBindingSite]string)
	for _, site := range sites {
		if typeName := r.rows[site]; typeName != "" {
			out[site] = typeName
		}
	}
	return out, nil
}

func TestResolveCallReturnTypesTypedContractIsQueryFree(t *testing.T) {
	store := &contractBatchStore{}
	idx := &Indexer{graph: store, logger: zap.NewNop()}
	reg := contracts.NewRegistry()
	reg.Add(contracts.Contract{
		ID:   "GET /ready",
		Role: contracts.RoleProvider,
		Type: contracts.ContractHTTP,
		Meta: map[string]any{
			"response_type": "repo/model.go::Ready",
			"response_envelope": []map[string]any{{
				"name": "ready",
				"type": "bool",
			}},
		},
	})

	idx.resolveCallReturnTypes(reg)
	if store.bindingCalls != 0 || store.findCalls != 0 {
		t.Fatalf("typed contract issued binding/name queries: bindings=%d names=%d", store.bindingCalls, store.findCalls)
	}
}

func TestReadSemanticBindingTypesUsesStoreThenOneProviderBatch(t *testing.T) {
	oldResolver := contracts.CurrentBindingResolver()
	defer contracts.SetBindingResolver(oldResolver)

	storeSite := graph.SemanticBindingSite{RepoPrefix: "repo", FilePath: "repo/a.go", Line: 10, Name: "a"}
	providerSite := graph.SemanticBindingSite{RepoPrefix: "repo", FilePath: "repo/b.go", Line: 20, Name: "b"}
	store := &contractBatchStore{
		bindingRows: map[graph.SemanticBindingSite]string{storeSite: "StoreType"},
	}
	resolver := &contractBatchResolver{
		rows: map[graph.SemanticBindingSite]string{providerSite: "ProviderType"},
	}
	contracts.SetBindingResolver(resolver)
	idx := &Indexer{graph: store, logger: zap.NewNop()}

	got := idx.readSemanticBindingTypes([]graph.SemanticBindingSite{providerSite, storeSite, storeSite})
	if got[storeSite] != "StoreType" || got[providerSite] != "ProviderType" {
		t.Fatalf("resolved bindings = %#v", got)
	}
	if store.bindingCalls != 1 || len(store.bindingBatches) != 1 || len(store.bindingBatches[0]) != 2 {
		t.Fatalf("store batches = %d %#v, want one deduplicated two-site batch", store.bindingCalls, store.bindingBatches)
	}
	if resolver.batchCalls != 1 || !reflect.DeepEqual(resolver.batchSizes, []int{1}) {
		t.Fatalf("provider batches = %d %v, want one batch containing only the store miss", resolver.batchCalls, resolver.batchSizes)
	}
	if resolver.legacyCalls != 0 {
		t.Fatalf("legacy per-binding lookups = %d, want 0", resolver.legacyCalls)
	}
}

func TestContractCallResolutionUsesTwoNameBatches(t *testing.T) {
	store := &contractBatchStore{
		nodesByName: map[string][]*graph.Node{
			"Fetch": {{
				ID:   "repo/service.go::Service.Fetch",
				Name: "Fetch",
				Kind: graph.KindMethod,
				Meta: map[string]any{"signature": "func Fetch() (*Thing, error)"},
			}},
			"List": {{
				ID:   "repo/service.go::Service.List",
				Name: "List",
				Kind: graph.KindMethod,
				Meta: map[string]any{"signature": "func List() ([]*Item, error)"},
			}},
			"Thing": {{ID: "repo/model.go::Thing", Name: "Thing", Kind: graph.KindType}},
			"Item":  {{ID: "repo/model.go::Item", Name: "Item", Kind: graph.KindType}},
		},
	}
	idx := &Indexer{graph: store, logger: zap.NewNop()}
	keys := []contractCallKey{
		{callExpr: "Fetch", repoPrefix: "repo"},
		{callExpr: "List", repoPrefix: "repo"},
	}

	raw := idx.resolveCallExprTypeNames(keys)
	typeKeys := make([]contractTypeNameKey, 0, len(raw))
	for key, result := range raw {
		typeKeys = append(typeKeys, contractTypeNameKey{typeName: result.typeName, repoPrefix: key.repoPrefix})
	}
	upgraded := idx.upgradeBareTypeNames(typeKeys)

	fetch := raw[keys[0]]
	if got := upgraded[contractTypeNameKey{typeName: fetch.typeName, repoPrefix: "repo"}]; got != "repo/model.go::Thing" || fetch.repeated || !fetch.pointer {
		t.Fatalf("Fetch = type %q repeated=%v pointer=%v", got, fetch.repeated, fetch.pointer)
	}
	list := raw[keys[1]]
	if got := upgraded[contractTypeNameKey{typeName: list.typeName, repoPrefix: "repo"}]; got != "repo/model.go::Item" || !list.repeated || !list.pointer {
		t.Fatalf("List = type %q repeated=%v pointer=%v", got, list.repeated, list.pointer)
	}
	if store.findCalls != 2 {
		t.Fatalf("FindNodesByNames calls = %d, want exactly 2 batches", store.findCalls)
	}
	if !reflect.DeepEqual(store.findBatches[0], []string{"Fetch", "List"}) {
		t.Fatalf("method batch = %v", store.findBatches[0])
	}
	if !reflect.DeepEqual(store.findBatches[1], []string{"Item", "Thing"}) {
		t.Fatalf("type batch = %v", store.findBatches[1])
	}
}

func TestUpgradeContractBareTypeRefsUsesOneNameBatch(t *testing.T) {
	store := &contractBatchStore{
		nodesByName: map[string][]*graph.Node{
			"Thing": {{ID: "repo/model.go::Thing", Name: "Thing", Kind: graph.KindType}},
			"Item":  {{ID: "repo/model.go::Item", Name: "Item", Kind: graph.KindType}},
		},
	}
	idx := &Indexer{graph: store, logger: zap.NewNop()}
	reg := contracts.NewRegistry()
	reg.Add(contracts.Contract{
		ID:         "GET /things",
		RepoPrefix: "repo",
		Meta: map[string]any{
			"request_type":  "Thing",
			"response_type": "Item",
		},
	})

	idx.upgradeContractBareTypeRefs(reg)
	if store.findCalls != 1 {
		t.Fatalf("FindNodesByNames calls = %d, want one", store.findCalls)
	}
	if !reflect.DeepEqual(store.findBatches[0], []string{"Item", "Thing"}) {
		t.Fatalf("type batch = %v", store.findBatches[0])
	}
	items := reg.ByID("GET /things")
	if len(items) != 1 {
		t.Fatalf("contracts = %d, want 1", len(items))
	}
	if got := items[0].Meta["request_type"]; got != "repo/model.go::Thing" {
		t.Fatalf("request_type = %v", got)
	}
	if got := items[0].Meta["response_type"]; got != "repo/model.go::Item" {
		t.Fatalf("response_type = %v", got)
	}
}
