package contracts

import (
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser/languages"
)

// TestInlineWrappers_NeighborTypeContamination guards the regression
// where consumer enrichment used a wide ±6/+14 callSiteWindow that
// spanned multiple neighbouring arrow-field functions in dense
// object-literal API declarations (api.ts pattern). The regex's
// FIRST `Promise<X>` match latched onto the wrong endpoint's type:
// `api.tools` ended up tagged with `api.health`'s `HealthResponse`,
// `api.stats` with `api.tools`'s `ToolInfo[]`, etc.
//
// Fix: enrichConsumerContract uses the enclosing function's body
// range (via consumerBodyRange) when SymbolID resolves to a fileNode.
// This test pins the correct attribution per arrow-field caller.
func TestInlineWrappers_NeighborTypeContamination(t *testing.T) {
	src := []byte(`async function serverFetch(path: string): Promise<Response> {
  return fetch(path)
}

export const api = {
  health: async (): Promise<HealthResponse> => {
    return await serverFetch('/v1/health')
  },
  tools: async (): Promise<ToolInfo[]> => {
    return await serverFetch('/v1/tools')
  },
  stats: async (): Promise<GraphStats> => {
    return await serverFetch('/v1/stats')
  },
}
`)

	ext := languages.NewTypeScriptExtractor()
	r, err := ext.Extract("web/src/lib/api.ts", src)
	if err != nil {
		t.Fatal(err)
	}
	g := graph.New()
	for _, n := range r.Nodes {
		g.AddNode(n)
	}
	for _, e := range r.Edges {
		g.AddEdge(e)
	}
	wrapper := g.GetNode("web/src/lib/api.ts::serverFetch")
	if wrapper == nil {
		t.Fatal("no serverFetch node")
	}
	for _, e := range r.Edges {
		if e.Kind == graph.EdgeCalls && e.To == "unresolved::serverFetch" {
			e.To = wrapper.ID
		}
	}
	g = graph.New()
	for _, n := range r.Nodes {
		g.AddNode(n)
	}
	for _, e := range r.Edges {
		g.AddEdge(e)
	}

	reg := NewRegistry()
	reg.Add(Contract{
		ID: "http::GET::/{p1}", Type: ContractHTTP, Role: RoleConsumer,
		SymbolID: wrapper.ID, FilePath: wrapper.FilePath, Line: wrapper.StartLine,
		Meta:     map[string]any{"path": "/{p1}", "method": "GET"},
	})
	read := func(n *graph.Node) ([]byte, bool) {
		if n.FilePath == "web/src/lib/api.ts" {
			return src, true
		}
		return nil, false
	}
	added := InlineWrappers(reg, g, read)

	type expected struct {
		responseType string
		repeated     bool
	}
	want := map[string]expected{ // path → expected (type, repeated)
		"/v1/health": {"HealthResponse", false},
		"/v1/tools":  {"ToolInfo", true}, // ToolInfo[] → bare ToolInfo + repeated=true
		"/v1/stats":  {"GraphStats", false},
	}
	for _, c := range added {
		path, _ := c.Meta["path"].(string)
		exp, ok := want[path]
		if !ok {
			continue
		}
		rt, _ := c.Meta["response_type"].(string)
		// resolveTypeInFile may prepend a path-qualified ID; the
		// type name is the suffix after `::`. Compare on the
		// trailing segment so the test isn't tied to the resolver.
		if i := strings.LastIndex(rt, "::"); i >= 0 {
			rt = rt[i+2:]
		}
		repeated, _ := c.Meta["response_repeated"].(bool)
		if rt != exp.responseType {
			t.Errorf("%s: response_type=%q, want %q (neighbour-type contamination regression)",
				path, rt, exp.responseType)
		}
		if repeated != exp.repeated {
			t.Errorf("%s: response_repeated=%v, want %v",
				path, repeated, exp.repeated)
		}
	}
}
