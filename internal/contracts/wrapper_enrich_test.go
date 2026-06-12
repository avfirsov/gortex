package contracts

import (
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// TestInlineWrappers_EnrichesConsumerSchema is the regression for the
// dashboard bug where wrapper-routed TypeScript consumers showed
// "REQUEST not declared" / "RESPONSE not declared" even though the
// caller annotated `const data: ToolResponse = await res.json()` and
// returned `Promise<string>`.
//
// Setup mirrors the real web/src/lib/api.ts:
//   - serverFetch is the wrapper, registered as a consumer with a
//     single-parameter path (`/${path}`) so seedWrappers picks it up
//   - callTool calls serverFetch with a literal path "/v1/tools/${name}"
//     and reads the response via `const data: ToolResponse = ...`
//
// After InlineWrappers runs, the inlined http::POST::/v1/tools/{p1}
// consumer contract MUST have request_type and response_type set.
func TestInlineWrappers_EnrichesConsumerSchema(t *testing.T) {
	src := []byte(`async function serverFetch(path: string, options?: RequestInit): Promise<Response> {
  const res = await fetch(` + "`" + `${SERVER_URL}${path}` + "`" + `, options)
  return res
}

async function callTool(name: string, args: Record<string, unknown> = {}): Promise<string> {
  const res = await serverFetch(` + "`" + `/v1/tools/${name}` + "`" + `, {
    method: 'POST',
    body: JSON.stringify({ arguments: args }),
  })
  const data: ToolResponse = await res.json()
  return data.text || ''
}
`)

	// Build the graph: caller (callTool) calls wrapper (serverFetch).
	g := graph.New()
	wrapperNode := &graph.Node{
		ID: "web/src/lib/api.ts::serverFetch", Name: "serverFetch",
		Kind: graph.KindFunction, FilePath: "web/src/lib/api.ts",
		Language: "typescript", StartLine: 1, EndLine: 4,
	}
	callerNode := &graph.Node{
		ID: "web/src/lib/api.ts::callTool", Name: "callTool",
		Kind: graph.KindFunction, FilePath: "web/src/lib/api.ts",
		Language: "typescript", StartLine: 6, EndLine: 13,
	}
	respTypeNode := &graph.Node{
		ID: "web/src/lib/api.ts::ToolResponse", Name: "ToolResponse",
		Kind: graph.KindType, FilePath: "web/src/lib/api.ts",
		Language: "typescript", StartLine: 0, EndLine: 0,
	}
	g.AddNode(wrapperNode)
	g.AddNode(callerNode)
	g.AddNode(respTypeNode)
	g.AddEdge(&graph.Edge{
		From: callerNode.ID, To: wrapperNode.ID,
		Kind: graph.EdgeCalls, FilePath: callerNode.FilePath, Line: 7,
	})

	// Seed the registry with the wrapper's parametric consumer
	// contract (path "/{path}"). seedWrappers picks this up because
	// the path matches isWrapperPath().
	reg := NewRegistry()
	reg.Add(Contract{
		ID: "http::GET::/{path}", Type: ContractHTTP, Role: RoleConsumer,
		SymbolID: wrapperNode.ID, FilePath: wrapperNode.FilePath, Line: 2,
		Meta: map[string]any{"path": "/{path}", "method": "GET"},
	})

	// SourceReader: return the test source for both nodes (same file).
	read := func(n *graph.Node) ([]byte, bool) {
		if n.FilePath == "web/src/lib/api.ts" {
			return src, true
		}
		return nil, false
	}

	added := InlineWrappers(reg, g, read)
	if len(added) == 0 {
		t.Fatal("InlineWrappers produced no contracts; expected the inlined callTool consumer")
	}

	// Locate the inlined contract for callTool.
	var c *Contract
	for i := range added {
		if added[i].SymbolID == callerNode.ID {
			c = &added[i]
			break
		}
	}
	if c == nil {
		t.Fatalf("no inlined contract for callTool; got: %+v", added)
	}

	// Schema enrichment MUST have populated response_type from the
	// `const data: ToolResponse = await res.json()` annotation.
	rt, _ := c.Meta["response_type"].(string)
	if rt == "" {
		t.Errorf("response_type empty; want ToolResponse-resolved id, got Meta=%+v", c.Meta)
	}
	if !strings.Contains(rt, "ToolResponse") {
		t.Errorf("response_type=%q; want a ToolResponse-resolved id", rt)
	}

	// schema_source should reflect that we extracted at least
	// some schema info (not "none").
	ss, _ := c.Meta["schema_source"].(string)
	if ss == "none" || ss == "" {
		t.Errorf("schema_source=%q; want extracted/partial", ss)
	}
}
