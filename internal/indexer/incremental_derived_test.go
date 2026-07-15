package indexer

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/contracts"
	"github.com/zzet/gortex/internal/graph"
)

func TestRunIncrementalDerivedPassesBodyOnlySkipsGlobalFamilies(t *testing.T) {
	mi := &MultiIndexer{
		graph:    graph.New(),
		indexers: map[string]*Indexer{},
		logger:   zap.NewNop(),
	}
	report := mi.RunIncrementalDerivedPasses(context.Background(), map[string]DerivedInvalidationPlan{
		"repo": {
			Files:         []string{"repo/a.go"},
			BodyOnlyFiles: 1,
		},
	})
	if report.Repos != 1 || report.Files != 1 || report.LegacyFallback {
		t.Fatalf("frontier report = %#v", report)
	}
	if report.Implements != 0 || report.Overrides != 0 || report.TestEdges != 0 ||
		report.Capability != 0 || report.Framework != 0 || report.ExternalCalls != 0 ||
		report.CrossRepo != 0 || report.Contracts != 0 {
		t.Fatalf("body-only edit ran a derived family: %#v", report)
	}
}

func TestContractSetsEqualIncludesAccuracyBearingLocations(t *testing.T) {
	base := contracts.Contract{ID: "http::GET::/x", Role: contracts.RoleProvider, FilePath: "a.go", SymbolID: "a.go::Serve", Line: 4}
	if !contractSetsEqual([]contracts.Contract{base}, []contracts.Contract{base}) {
		t.Fatal("identical contract sets compare different")
	}
	shifted := base
	shifted.Line++
	if contractSetsEqual([]contracts.Contract{base}, []contracts.Contract{shifted}) {
		t.Fatal("line shift was ignored by contract comparison")
	}
}

func TestContractSourceNeedsFullRefreshOnlyForCrossFileConstructs(t *testing.T) {
	if contractSourceNeedsFullRefresh("repo/a.go", "go", []byte("func f() {}")) {
		t.Fatal("ordinary Go source requested a full contract pass")
	}
	if !contractSourceNeedsFullRefresh("repo/router.py", "python", []byte("app.include_router(users)")) {
		t.Fatal("cross-file router mount did not request conservative fallback")
	}
	if !contractRefreshAlwaysFull("repo/go.mod") {
		t.Fatal("go.mod must keep dependency-contract fallback")
	}
}
