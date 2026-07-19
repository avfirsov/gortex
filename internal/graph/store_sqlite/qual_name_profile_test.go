package store_sqlite

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// BenchmarkGetNodesByQualNamesIndexed exercises the resolver's indexed,
// single-bind qualified-name warmup path. The fixture is deliberately bounded:
// it is large enough to compare representative 80- and 800-name pages while
// remaining cheap to construct for an explicit benchmark invocation. Standard
// go test runs compile this function but never build its fixture.
func BenchmarkGetNodesByQualNamesIndexed(b *testing.B) {
	const fixtureSize = 4096

	store, err := Open(filepath.Join(b.TempDir(), "qual-name-bench.sqlite"))
	if err != nil {
		b.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			b.Errorf("close benchmark store: %v", err)
		}
	}()

	nodes := make([]*graph.Node, fixtureSize)
	for i := range nodes {
		nodes[i] = &graph.Node{
			ID:       fmt.Sprintf("bench::node::%04d", i),
			Kind:     graph.KindFunction,
			Name:     "Symbol",
			QualName: fmt.Sprintf("bench.pkg.Symbol%04d", i),
			FilePath: "bench.go",
		}
	}
	store.AddBatch(nodes, nil)

	for _, requested := range []int{80, 800} {
		qualNames := make([]string, requested)
		for i := range qualNames {
			position := i * (fixtureSize - 1) / (requested - 1)
			qualNames[i] = nodes[position].QualName
		}

		b.Run(fmt.Sprintf("names_%d", requested), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got := store.GetNodesByQualNames(qualNames)
				if len(got) != requested {
					b.Fatalf("lookup returned %d nodes, want %d", len(got), requested)
				}
			}
		})
	}
}
