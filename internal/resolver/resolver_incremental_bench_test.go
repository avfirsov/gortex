package resolver

import (
	"fmt"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// BenchmarkResolveFileAndIncomingNoPending100K guards the watcher hot path:
// a generated asset with no unresolved forward/incoming edge must stay scoped
// to that file instead of rebuilding pass indexes over the whole graph.
func BenchmarkResolveFileAndIncomingNoPending100K(b *testing.B) {
	g := graph.New()
	for i := 0; i < 100_000; i++ {
		path := fmt.Sprintf("pkg/%06d.go", i)
		g.AddNode(&graph.Node{ID: path, Kind: graph.KindFile, Name: path, FilePath: path, Language: "go"})
	}
	g.AddNode(&graph.Node{ID: "results.json", Kind: graph.KindFile, Name: "results.json", FilePath: "results.json", Language: "json"})
	g.AddNode(&graph.Node{ID: "results.json::records", Kind: graph.KindVariable, Name: "records", FilePath: "results.json", Language: "json"})
	g.AddEdge(&graph.Edge{From: "results.json", To: "results.json::records", Kind: graph.EdgeDefines, FilePath: "results.json"})

	r := New(g)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stats := r.ResolveFileAndIncoming("results.json")
		if stats.Resolved != 0 || stats.Unresolved != 0 {
			b.Fatalf("unexpected resolver work: %+v", stats)
		}
	}
}
