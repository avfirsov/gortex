package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

func TestAnnotationNodeID(t *testing.T) {
	if got := AnnotationNodeID("ts", "Component"); got != "annotation::ts::Component" {
		t.Fatalf("got %q", got)
	}
}

func TestEmitAnnotationEdge_DedupsNodeAcrossEdges(t *testing.T) {
	result := &parser.ExtractionResult{}
	seen := map[string]bool{}

	EmitAnnotationEdge("a.ts::Foo", "ts", "Component", `{ selector: "x" }`, "a.ts", 5, result, seen)
	EmitAnnotationEdge("a.ts::Bar", "ts", "Component", "", "a.ts", 12, result, seen)

	annNodes := 0
	for _, n := range result.Nodes {
		if n.ID == "annotation::ts::Component" {
			annNodes++
		}
	}
	if annNodes != 1 {
		t.Fatalf("expected 1 annotation node, got %d", annNodes)
	}

	edges := 0
	var firstArgs string
	for _, e := range result.Edges {
		if e.Kind != graph.EdgeAnnotated {
			continue
		}
		edges++
		if e.From == "a.ts::Foo" {
			v, _ := e.Meta["args"].(string)
			if v != `{ selector: "x" }` {
				t.Fatalf("Foo edge args = %q", v)
			}
			firstArgs = v
		}
		if e.From == "a.ts::Bar" {
			if e.Meta != nil {
				if val, ok := e.Meta["args"]; ok && val != nil {
					t.Fatalf("Bar edge should have no args, got %v", val)
				}
			}
		}
	}
	if edges != 2 {
		t.Fatalf("expected 2 EdgeAnnotated edges, got %d", edges)
	}
	if firstArgs == "" {
		t.Fatalf("expected first args to be captured")
	}
}

func TestEmitAnnotationEdge_TruncatesLongArgs(t *testing.T) {
	result := &parser.ExtractionResult{}
	seen := map[string]bool{}
	long := make([]byte, annotationArgsMaxLen+50)
	for i := range long {
		long[i] = 'x'
	}
	EmitAnnotationEdge("a.py::route", "py", "app.route", string(long), "a.py", 1, result, seen)

	for _, e := range result.Edges {
		if e.Kind != graph.EdgeAnnotated {
			continue
		}
		args, _ := e.Meta["args"].(string)
		if len(args) > annotationArgsMaxLen+5 {
			t.Fatalf("args not truncated: %d chars", len(args))
		}
	}
}

func TestExtractParenArgs(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`Get("/users/:id")`, `"/users/:id"`},
		{`Module({ providers: [foo, bar] })`, `{ providers: [foo, bar] }`},
		{`Deprecated`, ""},
		{`Component()`, ""},
		{`derive(Debug, Clone)`, "Debug, Clone"},
		{`unbalanced(`, ""},
	}
	for _, c := range cases {
		if got := ExtractParenArgs(c.in); got != c.want {
			t.Fatalf("ExtractParenArgs(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
