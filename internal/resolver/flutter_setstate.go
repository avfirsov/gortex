package resolver

import (
	"sort"

	"github.com/zzet/gortex/internal/graph"
)

// flutterSetStateVia marks a synthesized Flutter setState→build reachability
// edge.
const flutterSetStateVia = "flutter.setstate"

// ResolveFlutterSetStateCalls is the framework-dispatch synthesizer for the
// Flutter widget re-build hop. `setState(() { … })` schedules the State's
// `build(...)` to re-run, but that hop is framework-internal — no static edge —
// so a flow dead-ends at setState even though everything `build` reaches is
// call-connected. This pass bridges it: for each State class that has a `build`
// method, it links every sibling method whose body calls `setState(` to that
// `build`. The setState call is the gate that keeps this to Flutter State
// classes — a plain class with a `build` method that never calls `setState`
// produces no edge.
//
// Over-approximation by design, full recompute and idempotent; edges ride at
// ast_inferred and carry synthesizer provenance. Returns the number of
// setState→build edges synthesized.
func ResolveFlutterSetStateCalls(g graph.Store) int {
	if g == nil {
		return 0
	}

	methods := nodesByKindsOrAll(g, graph.KindMethod)
	methodIDs := make([]string, 0, len(methods))
	for _, method := range methods {
		if method != nil {
			methodIDs = append(methodIDs, method.ID)
		}
	}
	outByMethod := g.GetOutEdgesByNodeIDs(methodIDs)
	classByMethod := map[string]string{}
	buildByClass := map[string]*graph.Node{}
	for _, n := range methods {
		if n == nil {
			continue
		}
		for _, e := range outByMethod[n.ID] {
			if e == nil || e.Kind != graph.EdgeMemberOf {
				continue
			}
			classByMethod[n.ID] = e.To
			if n.Name == "build" {
				buildByClass[e.To] = n
			}
			break
		}
	}
	if len(buildByClass) == 0 {
		return 0
	}

	var setStateMethods []*graph.Node
	for _, n := range methods {
		if n == nil {
			continue
		}
		build := buildByClass[classByMethod[n.ID]]
		if build == nil || build.ID == n.ID {
			continue
		}
		if !edgesCallSetState(outByMethod[n.ID]) {
			continue
		}
		setStateMethods = append(setStateMethods, n)
	}
	sort.Slice(setStateMethods, func(i, j int) bool {
		return setStateMethods[i].ID < setStateMethods[j].ID
	})

	var batch []*graph.Edge
	synthesized := 0
	for _, m := range setStateMethods {
		build := buildByClass[classByMethod[m.ID]]
		batch = append(batch, flutterSetStateEdge(m, build, classByMethod[m.ID]))
		synthesized++
	}

	if len(batch) > 0 {
		g.AddBatch(nil, batch)
	}
	return synthesized
}

// flutterSetStateEdge builds one setState-method → build synthesized edge.
func flutterSetStateEdge(from, build *graph.Node, class string) *graph.Edge {
	return &graph.Edge{
		From:            from.ID,
		To:              build.ID,
		Kind:            graph.EdgeCalls,
		FilePath:        from.FilePath,
		Line:            from.StartLine,
		Confidence:      0.6,
		ConfidenceLabel: graph.ConfidenceLabelFor(graph.EdgeCalls, 0.6),
		Origin:          graph.OriginASTInferred,
		Meta: map[string]any{
			"via":             flutterSetStateVia,
			"state_class":     class,
			MetaSynthesizedBy: SynthFlutterSetState,
			MetaProvenance:    ProvenanceHeuristic,
		},
	}
}
