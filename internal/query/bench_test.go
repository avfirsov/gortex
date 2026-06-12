package query

import (
	"testing"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/indexer"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
)

// buildTestEngine indexes the Gortex repo and returns an engine.
func buildTestEngine(b *testing.B) *Engine {
	b.Helper()
	g := graph.New()
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	idx := indexer.New(g, reg, config.IndexConfig{}, zap.NewNop())
	_, err := idx.Index("../..")
	if err != nil {
		b.Fatal(err)
	}
	return NewEngine(g)
}

func BenchmarkSearchSymbols(b *testing.B) {
	eng := buildTestEngine(b)
	b.ResetTimer()
	for b.Loop() {
		eng.SearchSymbols("Server", 20)
	}
}

func BenchmarkGetDependencies(b *testing.B) {
	eng := buildTestEngine(b)
	b.ResetTimer()
	for b.Loop() {
		eng.GetDependencies("internal/mcp/server.go::NewServer", QueryOptions{Depth: 2, Limit: 50})
	}
}

func BenchmarkGetDependents(b *testing.B) {
	eng := buildTestEngine(b)
	b.ResetTimer()
	for b.Loop() {
		eng.GetDependents("internal/graph/graph.go::Graph", QueryOptions{Depth: 3, Limit: 50})
	}
}

func BenchmarkGetCallers(b *testing.B) {
	eng := buildTestEngine(b)
	b.ResetTimer()
	for b.Loop() {
		eng.GetCallers("internal/graph/graph.go::Graph.AddNode", QueryOptions{Depth: 2, Limit: 50})
	}
}

func BenchmarkGetCallChain(b *testing.B) {
	eng := buildTestEngine(b)
	b.ResetTimer()
	for b.Loop() {
		eng.GetCallChain("cmd/gortex/serve.go::runServe", QueryOptions{Depth: 4, Limit: 50})
	}
}

func BenchmarkFindUsages(b *testing.B) {
	eng := buildTestEngine(b)
	b.ResetTimer()
	for b.Loop() {
		eng.FindUsages("internal/graph/graph.go::Graph")
	}
}

func BenchmarkGetCluster(b *testing.B) {
	eng := buildTestEngine(b)
	b.ResetTimer()
	for b.Loop() {
		eng.GetCluster("internal/mcp/server.go::NewServer", QueryOptions{Depth: 2, Limit: 50})
	}
}

func BenchmarkStats(b *testing.B) {
	eng := buildTestEngine(b)
	b.ResetTimer()
	for b.Loop() {
		eng.Stats()
	}
}
