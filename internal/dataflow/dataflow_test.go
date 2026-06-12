package dataflow

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func newGraphWithNodes(ids ...string) *graph.Graph {
	g := graph.New()
	for _, id := range ids {
		g.AddNode(&graph.Node{
			ID:       id,
			Kind:     graph.KindFunction,
			Name:     id,
			FilePath: "test.go",
		})
	}
	return g
}

func addEdge(g *graph.Graph, kind graph.EdgeKind, from, to string) {
	g.AddEdge(&graph.Edge{
		From:     from,
		To:       to,
		Kind:     kind,
		FilePath: "test.go",
		Origin:   graph.OriginASTResolved,
	})
}

func TestEngine_FlowBetween_Direct(t *testing.T) {
	g := newGraphWithNodes("A", "B")
	addEdge(g, graph.EdgeValueFlow, "A", "B")
	e := New(g)
	paths := e.FlowBetween("A", "B", 0, 0)
	if len(paths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(paths))
	}
	if paths[0].Length() != 1 || paths[0].IDs[0] != "A" || paths[0].IDs[1] != "B" {
		t.Errorf("unexpected path: %+v", paths[0])
	}
	if paths[0].Confidence <= 0 || paths[0].Confidence > 1 {
		t.Errorf("confidence out of range: %v", paths[0].Confidence)
	}
}

func TestEngine_FlowBetween_MultiHop(t *testing.T) {
	g := newGraphWithNodes("A", "B", "C", "D")
	addEdge(g, graph.EdgeValueFlow, "A", "B")
	addEdge(g, graph.EdgeArgOf, "B", "C")
	addEdge(g, graph.EdgeReturnsTo, "C", "D")
	e := New(g)
	paths := e.FlowBetween("A", "D", 0, 0)
	if len(paths) != 1 {
		t.Fatalf("expected 1 path, got %d: %+v", len(paths), paths)
	}
	if paths[0].Length() != 3 {
		t.Errorf("unexpected length: %d", paths[0].Length())
	}
}

func TestEngine_FlowBetween_NoPath(t *testing.T) {
	g := newGraphWithNodes("A", "B", "C")
	addEdge(g, graph.EdgeValueFlow, "A", "B")
	e := New(g)
	paths := e.FlowBetween("A", "C", 0, 0)
	if len(paths) != 0 {
		t.Errorf("expected no paths, got %+v", paths)
	}
}

func TestEngine_FlowBetween_DepthBound(t *testing.T) {
	// 5-hop chain: A → B → C → D → E → F
	g := newGraphWithNodes("A", "B", "C", "D", "E", "F")
	addEdge(g, graph.EdgeValueFlow, "A", "B")
	addEdge(g, graph.EdgeValueFlow, "B", "C")
	addEdge(g, graph.EdgeValueFlow, "C", "D")
	addEdge(g, graph.EdgeValueFlow, "D", "E")
	addEdge(g, graph.EdgeValueFlow, "E", "F")
	e := New(g)
	paths := e.FlowBetween("A", "F", 3, 0)
	if len(paths) != 0 {
		t.Errorf("max_depth=3 should not reach F at hop 5, got %+v", paths)
	}
	paths = e.FlowBetween("A", "F", 5, 0)
	if len(paths) != 1 {
		t.Errorf("max_depth=5 should reach F, got %+v", paths)
	}
}

func TestEngine_FlowBetween_MultiplePaths(t *testing.T) {
	// Diamond:  A → B → D
	//           A → C → D
	g := newGraphWithNodes("A", "B", "C", "D")
	addEdge(g, graph.EdgeValueFlow, "A", "B")
	addEdge(g, graph.EdgeValueFlow, "B", "D")
	addEdge(g, graph.EdgeValueFlow, "A", "C")
	addEdge(g, graph.EdgeValueFlow, "C", "D")
	e := New(g)
	paths := e.FlowBetween("A", "D", 0, 0)
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths through diamond, got %d: %+v", len(paths), paths)
	}
}

func TestEngine_FlowBetween_IgnoresNonDataflow(t *testing.T) {
	g := newGraphWithNodes("A", "B")
	// Calls is not a dataflow edge; the BFS must skip it.
	addEdge(g, graph.EdgeCalls, "A", "B")
	e := New(g)
	if got := e.FlowBetween("A", "B", 0, 0); len(got) != 0 {
		t.Errorf("expected no paths on call-only graph, got %+v", got)
	}
}

func TestEngine_FlowBetween_SelfPath(t *testing.T) {
	g := newGraphWithNodes("A")
	e := New(g)
	paths := e.FlowBetween("A", "A", 0, 0)
	if len(paths) != 1 || len(paths[0].IDs) != 1 {
		t.Errorf("expected one trivial self-path, got %+v", paths)
	}
}

func TestEngine_TaintPaths_PatternMatch(t *testing.T) {
	g := graph.New()
	addNamedFunc := func(id, name string) {
		g.AddNode(&graph.Node{
			ID:       id,
			Kind:     graph.KindFunction,
			Name:     name,
			FilePath: "test.go",
		})
	}
	addNamedFunc("pkg.GetEnv", "GetEnv")
	addNamedFunc("pkg.Mid", "mid")
	addNamedFunc("pkg.DBQuery", "DBQuery")
	addEdge(g, graph.EdgeValueFlow, "pkg.GetEnv", "pkg.Mid")
	addEdge(g, graph.EdgeArgOf, "pkg.Mid", "pkg.DBQuery")

	e := New(g)
	src := ParsePattern("name:Env")
	sink := ParsePattern("name:Query")
	findings := e.TaintPaths(src, sink, 0, 0)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Source.ID != "pkg.GetEnv" || f.Sink.ID != "pkg.DBQuery" {
		t.Errorf("unexpected pair: %s → %s", f.Source.ID, f.Sink.ID)
	}
	if len(f.Paths) == 0 {
		t.Errorf("path missing on finding")
	}
}

func TestEngine_TaintPaths_RanksByLength(t *testing.T) {
	g := graph.New()
	for _, name := range []string{"src1", "src2", "mid", "sink"} {
		g.AddNode(&graph.Node{
			ID: name, Kind: graph.KindFunction, Name: name, FilePath: "t.go",
		})
	}
	// src1 → sink (1 hop)
	addEdge(g, graph.EdgeValueFlow, "src1", "sink")
	// src2 → mid → sink (2 hops)
	addEdge(g, graph.EdgeValueFlow, "src2", "mid")
	addEdge(g, graph.EdgeValueFlow, "mid", "sink")

	e := New(g)
	src := ParsePattern("name:src")
	sink := ParsePattern("exact:sink")
	findings := e.TaintPaths(src, sink, 0, 0)
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}
	if findings[0].Source.ID != "src1" {
		t.Errorf("expected src1 first (1 hop), got %s", findings[0].Source.ID)
	}
}

func TestParsePattern_Combined(t *testing.T) {
	p := ParsePattern("name:foo kind:method")
	if p.Empty() {
		t.Fatal("pattern should match")
	}
	matchNode := func(name string, kind graph.NodeKind) bool {
		return p.matches(&graph.Node{Name: name, Kind: kind})
	}
	if !matchNode("foobar", graph.KindMethod) {
		t.Error("foobar method should match")
	}
	if matchNode("foobar", graph.KindFunction) {
		t.Error("function should not match kind:method clause")
	}
	if matchNode("baz", graph.KindMethod) {
		t.Error("baz should not match name:foo clause")
	}
}

func TestParsePattern_Exact(t *testing.T) {
	p := ParsePattern("exact:Foo")
	matchNode := func(name string) bool {
		return p.matches(&graph.Node{Name: name, Kind: graph.KindFunction})
	}
	if !matchNode("Foo") {
		t.Error("exact match must hit Foo")
	}
	if matchNode("FooBar") {
		t.Error("exact match must not hit FooBar")
	}
}

func TestParsePattern_Path(t *testing.T) {
	p := ParsePattern("path:cmd/ name:main")
	matchNode := func(name, path string) bool {
		return p.matches(&graph.Node{Name: name, FilePath: path, Kind: graph.KindFunction})
	}
	if !matchNode("main", "cmd/server/main.go") {
		t.Error("cmd/server/main.go::main should match")
	}
	if matchNode("main", "internal/server/main.go") {
		t.Error("internal path should not match cmd/ prefix")
	}
}
