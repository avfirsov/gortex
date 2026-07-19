package goanalysis

import (
	"fmt"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func funcIndexTestNode(id string, kind graph.NodeKind, start, end int) *graph.Node {
	return &graph.Node{ID: id, Kind: kind, StartLine: start, EndLine: end, FilePath: "repo/f.go"}
}

// The index must agree with findContainingFuncInNodes — the semantic
// reference — on containment and smallest-span size for every line,
// including the wrapper shape where a huge later-starting function spans a
// line that a tighter earlier function also contains.
func TestFileFuncIndexMatchesLinearReference(t *testing.T) {
	nodes := []*graph.Node{
		funcIndexTestNode("f1", graph.KindFunction, 10, 120),
		funcIndexTestNode("f2", graph.KindMethod, 30, 40),
		funcIndexTestNode("f3", graph.KindFunction, 99, 5000),
		funcIndexTestNode("f4", graph.KindMethod, 130, 150),
		funcIndexTestNode("f5", graph.KindFunction, 200, 210),
		// Non-callable kinds must be invisible to both implementations.
		funcIndexTestNode("t1", graph.KindType, 1, 9999),
		funcIndexTestNode("v1", graph.KindVariable, 100, 100),
		nil,
	}
	byFile := map[string][]*graph.Node{"repo/f.go": nodes}
	ix := buildFileFuncIndexes(byFile)["repo/f.go"]
	if ix == nil {
		t.Fatal("index not built for file with function nodes")
	}

	for line := 0; line <= 5100; line++ {
		want := findContainingFuncInNodes(nodes, line)
		got := ix.containing(line)
		if (want == nil) != (got == nil) {
			t.Fatalf("line %d: containment disagrees: linear=%v index=%v", line, want, got)
		}
		if want == nil {
			continue
		}
		wantSize := want.EndLine - want.StartLine
		gotSize := got.EndLine - got.StartLine
		if wantSize != gotSize {
			t.Fatalf("line %d: smallest-span disagrees: linear=%s(%d) index=%s(%d)",
				line, want.ID, wantSize, got.ID, gotSize)
		}
		if got.StartLine > line || line > got.EndLine {
			t.Fatalf("line %d: index returned non-containing span %s [%d,%d]",
				line, got.ID, got.StartLine, got.EndLine)
		}
	}
}

func TestFileFuncIndexNilSafety(t *testing.T) {
	var ix *fileFuncIndex
	if got := ix.containing(10); got != nil {
		t.Fatalf("nil index returned %v", got)
	}
	if built := buildFileFuncIndexes(map[string][]*graph.Node{
		"repo/only-types.go": {funcIndexTestNode("t", graph.KindType, 1, 100)},
	}); built["repo/only-types.go"] != nil {
		t.Fatal("file without callable nodes must not build an index")
	}
}

func TestFileFuncIndexManyFlatFunctions(t *testing.T) {
	// Flat file shape (no nesting): the leftward walk must terminate
	// immediately, and every line must land in its own function.
	nodes := make([]*graph.Node, 0, 500)
	for i := 0; i < 500; i++ {
		start := i*10 + 1
		nodes = append(nodes, funcIndexTestNode(fmt.Sprintf("f%03d", i), graph.KindFunction, start, start+8))
	}
	ix := buildFileFuncIndexes(map[string][]*graph.Node{"repo/f.go": nodes})["repo/f.go"]
	for i := 0; i < 500; i++ {
		line := i*10 + 5
		got := ix.containing(line)
		if got == nil || got.ID != fmt.Sprintf("f%03d", i) {
			t.Fatalf("line %d resolved to %v, want f%03d", line, got, i)
		}
	}
}
