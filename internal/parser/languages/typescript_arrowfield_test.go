package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// TestTypeScriptExtractor_ObjectLiteralArrowFields_GetsCallEdges
// guards the regression where calls inside `{ method: async () => ... }`
// arrow bodies were silently dropped. The call_expression matched
// the query but findEnclosingFunc found no covering function, so the
// EdgeCalls was never emitted — leaving downstream wrapper-inlining
// and find_usages blind to every endpoint method on objects like
// `export const api = { health: ..., tools: ... }`.
//
// After this fix, each arrow-as-pair-value gets its own KindFunction
// node, calls inside attribute correctly, and EdgeCalls fires.
func TestTypeScriptExtractor_ObjectLiteralArrowFields_GetsCallEdges(t *testing.T) {
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
	ext := NewTypeScriptExtractor()
	result, err := ext.Extract("api.ts", src)
	if err != nil {
		t.Fatal(err)
	}

	// Three arrow-field functions must show up as KindFunction nodes
	// named api.health / api.tools / api.stats.
	want := map[string]bool{
		"api.health": false,
		"api.tools":  false,
		"api.stats":  false,
	}
	for _, n := range result.Nodes {
		if n.Kind != graph.KindFunction {
			continue
		}
		if _, ok := want[n.Name]; ok {
			want[n.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing KindFunction node for arrow field %q", name)
		}
	}

	// Each arrow's `await serverFetch(...)` call must produce an
	// EdgeCalls from the arrow-field node to "unresolved::serverFetch".
	// The post-pass attributes calls to enclosing function based on
	// line range; without an arrow-field node, callerID was "" and
	// the edge was silently dropped (typescript.go:259 in the
	// findEnclosingFunc("") branch).
	calls := 0
	for _, e := range result.Edges {
		if e.Kind != graph.EdgeCalls {
			continue
		}
		if e.To != "unresolved::serverFetch" {
			continue
		}
		calls++
	}
	if calls != 3 {
		t.Errorf("EdgeCalls(serverFetch): want 3 (one per arrow field), got %d", calls)
	}
}

// TestTypeScriptExtractor_ArrowFieldNameDisambiguation guards the
// owner-qualified naming so two same-named arrow fields in different
// objects in one file don't collide on Node.Name.
func TestTypeScriptExtractor_ArrowFieldNameDisambiguation(t *testing.T) {
	src := []byte(`export const a = { run: () => 1 }
export const b = { run: () => 2 }
`)
	ext := NewTypeScriptExtractor()
	result, err := ext.Extract("dual.ts", src)
	if err != nil {
		t.Fatal(err)
	}
	saw := map[string]bool{}
	for _, n := range result.Nodes {
		if n.Kind != graph.KindFunction {
			continue
		}
		saw[n.Name] = true
	}
	if !saw["a.run"] {
		t.Errorf("missing a.run; nodes seen: %v", saw)
	}
	if !saw["b.run"] {
		t.Errorf("missing b.run; nodes seen: %v", saw)
	}
}
