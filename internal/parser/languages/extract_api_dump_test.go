package languages

import (
	"fmt"
	"os"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// TestDumpRealAPITSExtraction runs the TS extractor on the real
// web/src/lib/api.ts file and prints what nodes get emitted. Pure
// debug helper — not a regression. Skip on CI by default.
func TestDumpRealAPITSExtraction(t *testing.T) {
	if os.Getenv("DUMP_API_TS") == "" {
		t.Skip("set DUMP_API_TS=1 to run")
	}
	src, err := os.ReadFile("/Users/zzet/code/my/gortex/web/src/lib/api.ts")
	if err != nil {
		t.Skipf("api.ts not readable: %v", err)
	}
	ext := NewTypeScriptExtractor()
	r, err := ext.Extract("src/lib/api.ts", src)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("\nTotal nodes: %d, edges: %d\n", len(r.Nodes), len(r.Edges))
	fmt.Println("\nFunction nodes:")
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
