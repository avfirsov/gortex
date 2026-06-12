package analysis

import (
	"testing"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/indexer"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
)

func buildGraph(b *testing.B) *graph.Graph {
	b.Helper()
	g := graph.New()
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	idx := indexer.New(g, reg, config.IndexConfig{}, zap.NewNop())
	_, err := idx.Index("../..")
	if err != nil {
		b.Fatal(err)
	}
	return g
}

func BenchmarkDetectCommunities(b *testing.B) {
	g := buildGraph(b)
	b.ResetTimer()
	for b.Loop() {
		DetectCommunities(g)
	}
}

func BenchmarkDiscoverProcesses(b *testing.B) {
	g := buildGraph(b)
	b.ResetTimer()
	for b.Loop() {
		DiscoverProcesses(g)
	}
}
