package main

import (
	"fmt"
	"os"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser/languages"
)

func main() {
	src, err := os.ReadFile("/Users/zzet/code/my/gortex/web/src/lib/api.ts")
	if err != nil {
		panic(err)
	}
	ext := languages.NewTypeScriptExtractor()
	r, err := ext.Extract("src/lib/api.ts", src)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Total nodes: %d, edges: %d\n\n", len(r.Nodes), len(r.Edges))
	fmt.Println("Function nodes:")
	for _, n := range r.Nodes {
		if n.Kind == graph.KindFunction {
			fmt.Printf("  %s @%d-%d\n", n.Name, n.StartLine, n.EndLine)
		}
	}
	fmt.Println("\nEdgeCalls to serverFetch:")
	for _, e := range r.Edges {
		if e.Kind == graph.EdgeCalls && (e.To == "unresolved::serverFetch" || e.To == "src/lib/api.ts::serverFetch") {
			fmt.Printf("  from %s @%d\n", e.From, e.Line)
		}
	}
}
