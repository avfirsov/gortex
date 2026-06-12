package languages

import (
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func dataflowEdges(fix *extractedFixture, kind graph.EdgeKind) []*graph.Edge {
	return fix.edgesByKind[kind]
}

// hasEdge reports whether the fixture contains an edge of the given
// kind whose From / To match — empty string is a wildcard. Useful
// for asserting "an edge with these endpoints exists" without
// caring about line numbers or ordering.
func hasEdge(fix *extractedFixture, kind graph.EdgeKind, from, to string) bool {
	for _, e := range fix.edgesByKind[kind] {
		if from != "" && e.From != from {
			continue
		}
		if to != "" && e.To != to {
			continue
		}
		return true
	}
	return false
}

func TestGoDataflow_ParamFlowsToLocal(t *testing.T) {
	src := `package foo

func Handler(x int) int {
	y := x
	return y
}
`
	fix := runGoExtract(t, src)
	owner := "pkg/foo.go::Handler"

	// param x → local y@<line>
	flows := dataflowEdges(fix, graph.EdgeValueFlow)
	if len(flows) == 0 {
		t.Fatalf("no value_flow edges: %+v", fix.edgesByKind)
	}
	if !hasEdge(fix, graph.EdgeValueFlow, owner+"#param:x", "") {
		t.Errorf("missing value_flow from param x; got: %s", dumpEdges(flows))
	}
	// local y → owner via return
	if !hasEdgeWithMatcher(fix, graph.EdgeValueFlow, func(e *graph.Edge) bool {
		return strings.HasPrefix(e.From, owner+"#local:y@") && e.To == owner
	}) {
		t.Errorf("missing return-side value_flow into owner; got: %s", dumpEdges(flows))
	}
}

func TestGoDataflow_ChainedAssignments(t *testing.T) {
	src := `package foo

func Pipeline(input string) string {
	a := input
	b := a
	c := b
	return c
}
`
	fix := runGoExtract(t, src)
	owner := "pkg/foo.go::Pipeline"

	// param input → local a; a → b; b → c; c → owner via return.
	if !hasEdge(fix, graph.EdgeValueFlow, owner+"#param:input", "") {
		t.Errorf("missing flow from param input")
	}
	if !hasEdgeWithMatcher(fix, graph.EdgeValueFlow, func(e *graph.Edge) bool {
		return strings.HasPrefix(e.From, owner+"#local:a@") && strings.HasPrefix(e.To, owner+"#local:b@")
	}) {
		t.Errorf("missing flow a → b")
	}
	if !hasEdgeWithMatcher(fix, graph.EdgeValueFlow, func(e *graph.Edge) bool {
		return strings.HasPrefix(e.From, owner+"#local:b@") && strings.HasPrefix(e.To, owner+"#local:c@")
	}) {
		t.Errorf("missing flow b → c")
	}
	if !hasEdgeWithMatcher(fix, graph.EdgeValueFlow, func(e *graph.Edge) bool {
		return strings.HasPrefix(e.From, owner+"#local:c@") && e.To == owner
	}) {
		t.Errorf("missing flow c → owner")
	}
}

func TestGoDataflow_CallArgOf(t *testing.T) {
	src := `package foo

func Sink(s string) {}

func Source() string { return "hi" }

func Driver(input string) {
	Sink(input)
}
`
	fix := runGoExtract(t, src)
	driver := "pkg/foo.go::Driver"

	// arg_of: param input → unresolved::Sink (Meta arg_position=0).
	args := dataflowEdges(fix, graph.EdgeArgOf)
	if len(args) == 0 {
		t.Fatalf("no arg_of edges; got: %+v", fix.edgesByKind)
	}
	found := false
	for _, e := range args {
		if e.From != driver+"#param:input" {
			continue
		}
		if e.To != "unresolved::Sink" {
			continue
		}
		pos, _ := e.Meta["arg_position"].(int)
		if pos != 0 {
			t.Errorf("arg_position = %v, want 0", e.Meta["arg_position"])
		}
		found = true
	}
	if !found {
		t.Errorf("missing arg_of(param:input → unresolved::Sink); got: %s", dumpEdges(args))
	}
}

func TestGoDataflow_CallReturnsTo(t *testing.T) {
	src := `package foo

func Source() string { return "hi" }

func Driver() {
	v := Source()
	_ = v
}
`
	fix := runGoExtract(t, src)
	driver := "pkg/foo.go::Driver"

	// returns_to: From=ownerID (placeholder), To=#local:v@<line>,
	// Meta carries the unresolved::Source target.
	rets := dataflowEdges(fix, graph.EdgeReturnsTo)
	if len(rets) == 0 {
		t.Fatalf("no returns_to edges; got: %+v", fix.edgesByKind)
	}
	found := false
	for _, e := range rets {
		if e.From != driver {
			continue
		}
		if !strings.HasPrefix(e.To, driver+"#local:v@") {
			continue
		}
		if e.Meta["callee_target"] != "unresolved::Source" {
			t.Errorf("callee_target = %v, want unresolved::Source", e.Meta["callee_target"])
		}
		if e.Meta["returns_to_call"] != true {
			t.Errorf("returns_to_call meta missing; got: %v", e.Meta)
		}
		found = true
	}
	if !found {
		t.Errorf("missing returns_to placeholder for v := Source(); got: %s", dumpEdges(rets))
	}
}

func TestGoDataflow_RangeClause(t *testing.T) {
	src := `package foo

func Range(items []string) {
	for _, item := range items {
		_ = item
	}
}
`
	fix := runGoExtract(t, src)
	owner := "pkg/foo.go::Range"

	// items → local item.
	if !hasEdgeWithMatcher(fix, graph.EdgeValueFlow, func(e *graph.Edge) bool {
		return e.From == owner+"#param:items" && strings.HasPrefix(e.To, owner+"#local:item@")
	}) {
		t.Errorf("missing range flow items → item; got: %s", dumpEdges(dataflowEdges(fix, graph.EdgeValueFlow)))
	}
}

func TestGoDataflow_NestedCallArgs(t *testing.T) {
	src := `package foo

func Outer(s string) {}
func Inner(s string) string { return s }

func Driver(in string) {
	Outer(Inner(in))
}
`
	fix := runGoExtract(t, src)
	driver := "pkg/foo.go::Driver"

	// arg_of for Inner: param in → unresolved::Inner @ pos 0.
	if !hasEdgeWithMatcher(fix, graph.EdgeArgOf, func(e *graph.Edge) bool {
		return e.From == driver+"#param:in" && e.To == "unresolved::Inner"
	}) {
		t.Errorf("missing arg_of(param:in → Inner); got: %s", dumpEdges(dataflowEdges(fix, graph.EdgeArgOf)))
	}
	// arg_of for Outer: source is Inner's result (callee text) →
	// unresolved::Outer @ pos 0.
	if !hasEdgeWithMatcher(fix, graph.EdgeArgOf, func(e *graph.Edge) bool {
		return e.From == "unresolved::Inner" && e.To == "unresolved::Outer"
	}) {
		t.Errorf("missing arg_of(Inner → Outer); got: %s", dumpEdges(dataflowEdges(fix, graph.EdgeArgOf)))
	}
}

func TestGoDataflow_MethodCall(t *testing.T) {
	src := `package foo

type Sink struct{}

func (s *Sink) Write(data string) {}

func Driver(s *Sink, payload string) {
	s.Write(payload)
}
`
	fix := runGoExtract(t, src)
	driver := "pkg/foo.go::Driver"

	if !hasEdgeWithMatcher(fix, graph.EdgeArgOf, func(e *graph.Edge) bool {
		return e.From == driver+"#param:payload" && e.To == "unresolved::*.Write"
	}) {
		t.Errorf("missing arg_of(param:payload → *.Write); got: %s", dumpEdges(dataflowEdges(fix, graph.EdgeArgOf)))
	}
}

func TestGoDataflow_NoEdgesFromConstants(t *testing.T) {
	src := `package foo

func Sink(s string) {}

func Driver() {
	Sink("hello")
}
`
	fix := runGoExtract(t, src)
	// String literal shouldn't produce an arg_of edge — there's no
	// upstream source. Verify count is zero / minimal.
	args := dataflowEdges(fix, graph.EdgeArgOf)
	for _, e := range args {
		if e.From == `"hello"` || strings.Contains(e.From, "literal") {
			t.Errorf("unexpected arg_of from literal: %+v", e)
		}
	}
}

func TestGoDataflow_ClosureBodyNotRecursed(t *testing.T) {
	src := `package foo

func Outer(x int) {
	f := func(y int) int {
		return x + y
	}
	_ = f
}
`
	fix := runGoExtract(t, src)
	outer := "pkg/foo.go::Outer"

	// The outer function should still get a value_flow from the
	// short-var-decl (RHS = func_literal — no source — so nothing
	// emitted there) but should NOT get any flows attributing the
	// closure body's `x + y` to Outer.
	for _, e := range dataflowEdges(fix, graph.EdgeValueFlow) {
		if e.From == outer+"#param:x" && strings.HasPrefix(e.To, outer+"#local:y@") {
			t.Errorf("closure body leaked to outer scope: %+v", e)
		}
	}
}

func hasEdgeWithMatcher(fix *extractedFixture, kind graph.EdgeKind, fn func(*graph.Edge) bool) bool {
	for _, e := range fix.edgesByKind[kind] {
		if fn(e) {
			return true
		}
	}
	return false
}

func dumpEdges(edges []*graph.Edge) string {
	var b strings.Builder
	for i, e := range edges {
		if i > 0 {
			b.WriteString(" | ")
		}
		b.WriteString(string(e.Kind))
		b.WriteString(" ")
		b.WriteString(e.From)
		b.WriteString(" -> ")
		b.WriteString(e.To)
	}
	return b.String()
}
