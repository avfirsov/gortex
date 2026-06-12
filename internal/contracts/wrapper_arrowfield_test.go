package contracts

import (
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser/languages"
)

// TestInlineWrappers_TSObjectLiteralArrowFields_FullPipeline exercises
// the complete chain end-to-end:
//
//  1. Run the TypeScript extractor on api.ts-shaped source (a wrapper
//     `serverFetch` plus an `export const api = { health: () => ..., ... }`
//     object whose fields each call the wrapper with a literal path).
//  2. Build the graph from result.Nodes and result.Edges.
//  3. Seed the registry with the wrapper's parametric consumer contract
//     (the shape HTTPExtractor would emit).
//  4. Run InlineWrappers.
//
// Expectation: one consumer contract per arrow-field caller —
// http::GET::/v1/health, /v1/tools, /v1/stats. Without the
// emitArrowField fix these calls were silently dropped at
// findEnclosingFunc and InlineWrappers saw zero callers.
//
// This is the regression for the dashboard bug "web shows only 2
// contracts despite 20+ endpoints".
func TestInlineWrappers_TSObjectLiteralArrowFields_FullPipeline(t *testing.T) {
	src := []byte(`async function serverFetch(path: string): Promise<Response> {
  return fetch(path)
}

export const api = {
  health: async (): Promise<Response> => {
    return await serverFetch('/v1/health')
  },
  tools: async (): Promise<Response> => {
    return await serverFetch('/v1/tools')
  },
  stats: async (): Promise<Response> => {
    return await serverFetch('/v1/stats')
  },
}
`)

	// Step 1: Run the TS extractor.
	ext := languages.NewTypeScriptExtractor()
	r, err := ext.Extract("web/src/lib/api.ts", src)
	if err != nil {
		t.Fatal(err)
	}

	// Step 2: Build the graph.
	g := graph.New()
	for _, n := range r.Nodes {
		g.AddNode(n)
	}
	for _, e := range r.Edges {
		g.AddEdge(e)
	}

	// Confirm serverFetch landed as a graph node so InlineWrappers
	// can target it.
	wrapper := g.GetNode("web/src/lib/api.ts::serverFetch")
	if wrapper == nil {
		t.Fatal("serverFetch wrapper missing from graph")
	}

	// The TS resolver normally rewrites unresolved::serverFetch edges
	// to point at the actual function node. In this isolated test we
	// don't run the resolver, so manually retarget those edges
	// in-place — InlineWrappers reads g.GetInEdges by To-id, not
	// EdgeKind specifics.
	for _, e := range r.Edges {
		if e.Kind == graph.EdgeCalls && e.To == "unresolved::serverFetch" {
			e.To = wrapper.ID
		}
	}
	// Rebuild graph to reflect the retarget.
	g = graph.New()
	for _, n := range r.Nodes {
		g.AddNode(n)
	}
	for _, e := range r.Edges {
		g.AddEdge(e)
	}

	// Step 3: Seed the wrapper's parametric consumer contract — the
	// shape HTTPExtractor would emit for `fetch(path)` inside
	// serverFetch's body (path is parametric → "/{p1}").
	reg := NewRegistry()
	reg.Add(Contract{
		ID:       "http::GET::/{p1}",
		Type:     ContractHTTP,
		Role:     RoleConsumer,
		SymbolID: wrapper.ID,
		FilePath: wrapper.FilePath,
		Line:     wrapper.StartLine,
		Meta:     map[string]any{"path": "/{p1}", "method": "GET"},
	})

	// Step 4: SourceReader returns the test source for any node
	// in our test file.
	read := func(n *graph.Node) ([]byte, bool) {
		if n.FilePath == "web/src/lib/api.ts" {
			return src, true
		}
		return nil, false
	}

	added := InlineWrappers(reg, g, read)

	// Expectations: three concrete consumer contracts (one per
	// arrow-field) plus possibly the original parametric seed.
	wantPaths := map[string]bool{
		"/v1/health": false,
		"/v1/tools":  false,
		"/v1/stats":  false,
	}
	for _, c := range added {
		path, _ := c.Meta["path"].(string)
		if _, ok := wantPaths[path]; ok {
			wantPaths[path] = true
		}
	}
	for p, found := range wantPaths {
		if !found {
			t.Errorf("missing inlined contract for path %q (added=%d, see %s)",
				p, len(added), strings.Join(addedPaths(added), ", "))
		}
	}

	// And the SymbolID of each inlined contract should reference an
	// arrow-field function node, not the file or the wrapper itself —
	// that's the proof the call graph correctly attributed the calls.
	for _, c := range added {
		path, _ := c.Meta["path"].(string)
		if _, ok := wantPaths[path]; !ok {
			continue
		}
		if c.SymbolID == wrapper.ID {
			t.Errorf("inlined contract %q points back at the wrapper (caller attribution lost)", path)
		}
	}
}

func addedPaths(cs []Contract) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		if p, _ := c.Meta["path"].(string); p != "" {
			out = append(out, p)
		}
	}
	return out
}
