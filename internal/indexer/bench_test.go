package indexer

import (
	"testing"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
)

// BenchmarkIndex_Self indexes the Gortex repository itself.
func BenchmarkIndex_Self(b *testing.B) {
	for b.Loop() {
		g := graph.New()
		reg := parser.NewRegistry()
		languages.RegisterAll(reg)
		idx := New(g, reg, config.IndexConfig{}, zap.NewNop())
		_, err := idx.Index("../..")
		if err != nil {
			b.Fatal(err)
		}
	}
}
