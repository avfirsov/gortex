package indexer

import (
	"path/filepath"
	"sort"
	"testing"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/contracts"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDIContracts_NestJSFixture indexes the NestJS DI fixture and
// asserts the expected set of di::<token> contracts shows up in the
// registry with matched provider/consumer pairs. Guards against
// regression in either the TypeScript extractor (which emits the
// underlying edges) or the indexer post-pass that materialises them.
func TestDIContracts_NestJSFixture(t *testing.T) {
	// Walk up from the package to the repo root and point at the fixture.
	root, err := filepath.Abs("../../bench/fixtures/di/nestjs")
	require.NoError(t, err)

	g := graph.New()
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	cfg := config.Default()
	idx := New(g, reg, cfg.Index, zap.NewNop())
	_, err = idx.Index(root)
	require.NoError(t, err)

	cr := idx.ContractRegistry()
	require.NotNil(t, cr)

	byID := make(map[string]map[contracts.Role][]contracts.Contract)
	for _, c := range cr.All() {
		if c.Type != contracts.ContractDI {
			continue
		}
		m, ok := byID[c.ID]
		if !ok {
			m = make(map[contracts.Role][]contracts.Contract)
			byID[c.ID] = m
		}
		m[c.Role] = append(m[c.Role], c)
	}

	// Explicit-token bindings (@Inject(TOKEN) paired with
	// { provide: TOKEN, useValue/useFactory: ... }) get both a
	// provider and a consumer contract record.
	wantPaired := []string{
		"di::DATABASE_URL",
		"di::FEATURE_FLAGS",
		"di::DB_CONNECTION",
	}
	for _, want := range wantPaired {
		m, ok := byID[want]
		require.True(t, ok, "missing DI contract %s", want)
		assert.NotEmpty(t, m[contracts.RoleProvider], "%s has no provider", want)
		assert.NotEmpty(t, m[contracts.RoleConsumer], "%s has no consumer", want)
	}

	// useClass bindings produce a provider record but no consumer
	// record — consumers pick the abstract up implicitly via a typed
	// constructor param, which the resolver's useClass-preference pass
	// already handles end-to-end. Surfacing them in the contracts tool
	// would require walking constructor type annotations; out of scope
	// for this pass.
	wantProviderOnly := []string{"di::Notifier"}
	for _, want := range wantProviderOnly {
		m, ok := byID[want]
		require.True(t, ok, "missing useClass provider %s", want)
		assert.NotEmpty(t, m[contracts.RoleProvider], "%s has no provider", want)
	}

	// Debug print sorted summary on failure so curating the fixture is easy.
	if t.Failed() {
		var ids []string
		for id := range byID {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			t.Logf("  %s provides=%d consumers=%d", id, len(byID[id][contracts.RoleProvider]), len(byID[id][contracts.RoleConsumer]))
		}
	}
}

// TestDIContracts_MatcherPairing verifies that every DI consumer in
// the fixture has at least one matching provider sharing the same
// contract ID. Without this pairing the contracts MCP tool's orphan-
// detection view would always show every DI binding as two unpaired
// halves.
func TestDIContracts_MatcherPairing(t *testing.T) {
	root, err := filepath.Abs("../../bench/fixtures/di/nestjs")
	require.NoError(t, err)

	g := graph.New()
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	cfg := config.Default()
	idx := New(g, reg, cfg.Index, zap.NewNop())
	_, err = idx.Index(root)
	require.NoError(t, err)

	cr := idx.ContractRegistry()
	require.NotNil(t, cr)

	providers := map[string]bool{}
	var consumers []contracts.Contract
	for _, c := range cr.All() {
		if c.Type != contracts.ContractDI {
			continue
		}
		if c.Role == contracts.RoleProvider {
			providers[c.ID] = true
			continue
		}
		if c.Role == contracts.RoleConsumer {
			consumers = append(consumers, c)
		}
	}

	for _, c := range consumers {
		if !providers[c.ID] {
			t.Errorf("DI consumer %s (symbol %s) has no matching provider", c.ID, c.SymbolID)
		}
	}
}
