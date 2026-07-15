package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestRustExtractorCapturesEffectiveGenericBounds(t *testing.T) {
	tests := []struct {
		name string
		src  string
		kind graph.NodeKind
	}{
		{
			name: "inline impl bound",
			kind: graph.KindMethod,
			src: `
trait Matcher { fn is_match(&self) -> bool; }
struct Runner<M> { matcher: M }
impl<M: Matcher> Runner<M> {
    fn run(&self, matcher: M) -> bool { matcher.is_match() }
}
`,
		},
		{
			name: "impl where bound",
			kind: graph.KindMethod,
			src: `
trait Matcher { fn is_match(&self) -> bool; }
struct Runner<M> { matcher: M }
impl<M> Runner<M> where M: Matcher {
    fn run(&self, matcher: M) -> bool { matcher.is_match() }
}
`,
		},
		{
			name: "function where bound",
			kind: graph.KindFunction,
			src: `
trait Matcher { fn is_match(&self) -> bool; }
fn run<M>(matcher: M) -> bool where M: Matcher { matcher.is_match() }
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := NewRustExtractor().Extract("src/lib.rs", []byte(tt.src))
			if err != nil {
				t.Fatalf("extract: %v", err)
			}
			var run *graph.Node
			for _, node := range result.Nodes {
				if node.Name == "run" && node.Kind == tt.kind {
					run = node
					break
				}
			}
			if run == nil {
				t.Fatalf("run node (%s) not emitted", tt.kind)
			}
			if got := rustTestTypeParamBound(run.Meta["type_params"], "M"); got != "Matcher" {
				t.Fatalf("M bound = %q, want Matcher; meta=%v", got, run.Meta)
			}

			var call *graph.Edge
			for _, edge := range result.Edges {
				if edge.From == run.ID && edge.Kind == graph.EdgeCalls && edge.To == "unresolved::*.is_match" {
					call = edge
					break
				}
			}
			if call == nil {
				t.Fatalf("generic receiver call not emitted for %s", run.ID)
			}
			if got, _ := call.Meta["receiver_type"].(string); got != "M" {
				t.Fatalf("receiver_type = %q, want M; meta=%v", got, call.Meta)
			}
		})
	}
}

func rustTestTypeParamBound(value any, name string) string {
	entries, _ := value.([]map[string]string)
	for _, entry := range entries {
		if entry["name"] == name {
			return entry["bound"]
		}
	}
	return ""
}
