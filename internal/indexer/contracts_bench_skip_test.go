package indexer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/contracts"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
)

// TestContracts_TagBenchFixtures asserts that contracts extracted from
// synthetic test/bench fixture files land in the registry tagged with
// is_test=true and a test_source category, while production contracts
// stay untagged. The dashboard uses these tags to hide synthetic rows
// by default; drift checks rely on the contracts still being present
// to flag a stale test pinned to an obsolete production contract.
func TestContracts_TagBenchFixtures(t *testing.T) {
	dir := t.TempDir()

	// Production file: a real route, expected untagged.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main

import "net/http"

func setup(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/users", listUsers)
}

func listUsers(http.ResponseWriter, *http.Request) {}
`), 0o644))

	// Fixture file under bench/fixtures/: same shape, expected tagged.
	fixDir := filepath.Join(dir, "bench", "fixtures", "synthetic")
	require.NoError(t, os.MkdirAll(fixDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fixDir, "fake.go"), []byte(`package synthetic

import "net/http"

func register(mux *http.ServeMux) {
	mux.HandleFunc("GET /bench-only/synthetic", h)
}

func h(http.ResponseWriter, *http.Request) {}
`), 0o644))

	g := graph.New()
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	cfg := config.Default()
	idx := New(g, reg, cfg.Index, zap.NewNop())
	_, err := idx.Index(dir)
	require.NoError(t, err)

	cr := idx.ContractRegistry()
	require.NotNil(t, cr)

	// Capture the slice once — Registry.All() iterates a Go map under
	// the hood, so iteration order is randomized between calls. Holding
	// each Contract by value (Meta is a reference, so this still sees
	// the live map) keeps the assertion stable.
	var prod, fixture contracts.Contract
	var prodFound, fixtureFound bool
	for _, c := range cr.All() {
		if c.Type != contracts.ContractHTTP {
			continue
		}
		switch {
		case strings.Contains(c.ID, "/v1/users"):
			prod, prodFound = c, true
		case strings.Contains(c.ID, "/bench-only/synthetic"):
			fixture, fixtureFound = c, true
		}
	}

	if !prodFound {
		t.Fatalf("expected production HTTP contract for /v1/users; not found")
	}
	if v, ok := prod.Meta["is_test"]; ok {
		t.Errorf("production contract should not carry is_test; got %v", v)
	}

	if !fixtureFound {
		t.Fatalf("expected fixture HTTP contract for /bench-only/synthetic; not found")
	}
	isTest, _ := fixture.Meta["is_test"].(bool)
	if !isTest {
		t.Errorf("fixture contract missing is_test=true; meta=%v", fixture.Meta)
	}
	if got, _ := fixture.Meta["test_source"].(string); got != "bench_fixtures" {
		t.Errorf("fixture contract test_source = %q, want %q", got, "bench_fixtures")
	}
}
