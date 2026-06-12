package indexer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

// indexAll indexes a single-file Go fixture and runs the global
// resolve + dataflow materialisation pass. Returns the graph for
// assertions.
func indexAll(t *testing.T, src string) graph.Store {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644))

	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)
	idx.ResolveAll()
	return g
}

// findEdges returns all edges matching the predicate.
func findEdges(g graph.Store, kind graph.EdgeKind, match func(*graph.Edge) bool) []*graph.Edge {
	var out []*graph.Edge
	for _, e := range g.AllEdges() {
		if e.Kind != kind {
			continue
		}
		if match(e) {
			out = append(out, e)
		}
	}
	return out
}

// TestMaterializeDataflowParams_ArgOfLifted verifies the post-pass
// rewrites EdgeArgOf targets from the function node to the actual
// param node at the recorded position.
func TestMaterializeDataflowParams_ArgOfLifted(t *testing.T) {
	src := `package main

func Sink(payload string) {}

func Driver(input string) {
	Sink(input)
}
`
	g := indexAll(t, src)

	// Look up the resolved Driver function ID — we only know the
	// suffix, the prefix is whatever IndexDirectory uses.
	driverID := findFuncID(t, g, "Driver")
	sinkID := findFuncID(t, g, "Sink")

	args := findEdges(g, graph.EdgeArgOf, func(e *graph.Edge) bool {
		return e.From == driverID+"#param:input"
	})
	if len(args) == 0 {
		t.Fatalf("no arg_of edges from Driver#param:input; have edges:\n%s", dumpAllEdges(g))
	}
	// The post-pass should have lifted To from sinkID itself to
	// sinkID#param:payload.
	want := sinkID + "#param:payload"
	for _, e := range args {
		if e.To == want {
			return
		}
	}
	t.Fatalf("arg_of not lifted to %q; got: %s", want, dumpEdges(args))
}

// TestMaterializeDataflowParams_ReturnsToLifted verifies the post-pass
// rewrites EdgeReturnsTo From from the placeholder caller ID to
// the resolved callee.
func TestMaterializeDataflowParams_ReturnsToLifted(t *testing.T) {
	src := `package main

func Source() string { return "hi" }

func Driver() {
	v := Source()
	_ = v
}
`
	g := indexAll(t, src)

	driverID := findFuncID(t, g, "Driver")
	sourceID := findFuncID(t, g, "Source")

	rets := findEdges(g, graph.EdgeReturnsTo, func(e *graph.Edge) bool {
		return strings.HasPrefix(e.To, driverID+"#local:v@")
	})
	if len(rets) == 0 {
		t.Fatalf("no returns_to edges; have edges:\n%s", dumpAllEdges(g))
	}
	for _, e := range rets {
		if e.From == sourceID {
			return
		}
	}
	t.Fatalf("returns_to.From not lifted to %q; got: %s", sourceID, dumpEdges(rets))
}

// TestMaterializeDataflowParams_CrossFileBinding asserts dataflow
// edges connect across files: `Driver` (in driver.go) calls
// `Sink` (in sink.go) and the post-pass binds the arg_of edge to
// Sink's param node, even though neither side has the other's
// AST visible at extraction time.
func TestMaterializeDataflowParams_CrossFileBinding(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sink.go"), []byte(`package main

func Sink(payload string) {}
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "driver.go"), []byte(`package main

func Driver(input string) {
	Sink(input)
}
`), 0o644))

	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)
	idx.ResolveAll()

	driverID := findFuncID(t, g, "Driver")
	sinkID := findFuncID(t, g, "Sink")
	want := sinkID + "#param:payload"

	args := findEdges(g, graph.EdgeArgOf, func(e *graph.Edge) bool {
		return e.From == driverID+"#param:input" && e.To == want
	})
	if len(args) == 0 {
		t.Fatalf("missing cross-file arg_of(Driver#param:input → %s); have:\n%s", want, dumpAllEdges(g))
	}
}

// TestMaterializeDataflowParams_NoStrandedPlaceholders asserts the
// post-pass leaves no stranded placeholders for resolvable callees.
func TestMaterializeDataflowParams_NoStrandedPlaceholders(t *testing.T) {
	src := `package main

func A(x int) int { return x }
func B(y int) int { return y }

func Driver(z int) int {
	return A(B(z))
}
`
	g := indexAll(t, src)

	// All arg_of edges that reference resolved callees should have
	// been lifted to a param-node target.
	driverID := findFuncID(t, g, "Driver")
	args := findEdges(g, graph.EdgeArgOf, func(e *graph.Edge) bool {
		return e.From == driverID+"#param:z" || e.From == ""
	})
	for _, e := range args {
		if !strings.Contains(e.To, "#param:") {
			t.Errorf("expected arg_of.To to contain #param:; got %q", e.To)
		}
	}
}

func findFuncID(t *testing.T, g graph.Store, name string) string {
	t.Helper()
	candidates := g.FindNodesByName(name)
	for _, n := range candidates {
		if n.Kind == graph.KindFunction || n.Kind == graph.KindMethod {
			return n.ID
		}
	}
	t.Fatalf("function %q not found", name)
	return ""
}

func dumpAllEdges(g graph.Store) string {
	var b strings.Builder
	for _, e := range g.AllEdges() {
		b.WriteString(string(e.Kind))
		b.WriteString(" ")
		b.WriteString(e.From)
		b.WriteString(" -> ")
		b.WriteString(e.To)
		b.WriteString("\n")
	}
	return b.String()
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
