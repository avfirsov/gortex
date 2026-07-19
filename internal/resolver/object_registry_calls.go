package resolver

import (
	"strconv"
	"strings"

	"github.com/zzet/gortex/internal/graph"
)

// objectRegistryVia is the Meta["via"] tag the JS/TS extractors stamp on an
// object-literal command-registry dispatch placeholder:
// `this.commands = {[Cmd.ADD]: AddCommand}; new this.commands[cmd]().execute()`.
const objectRegistryVia = "object-registry"

// ResolveObjectRegistryCalls binds an object-literal command/handler
// registry dispatch to each registered handler's invoked method. The
// extractor records `binding → [HandlerClass]` from the object literal and
// stamps a placeholder EdgeCalls from the dispatching function to
// `unresolved::*.<HandlerClass>` per registered class, tagged with
// Meta["via"]="object-registry" + registry_value (the class) +
// registry_method (execute/run/handle when invoked at the dispatch site).
// This pass resolves each class to its method node (callable-only,
// same-file then unique disambiguation) at the heuristic confidence tier.
//
// Where it lands a real edge it marks the dispatch site claimed, so the
// opt-in speculative-dispatch pass (which runs after) does not also mint a
// hidden best-guess edge for the same computed-member call.
//
// Returns the number of placeholders landed on a real method.
func ResolveObjectRegistryCalls(g graph.Store) int {
	if g == nil {
		return 0
	}
	// Placeholders are collected BEFORE the indexes are built: a workspace
	// with no object-registry placeholders (the common case) pays only this
	// scan — the EdgeMemberOf scan, the whole-graph method/function index,
	// and the dyn_shape site index are skipped, and with zero placeholders
	// the resolve loop below is vacuously identical. The indexes built after
	// collection see the identical graph, so the run case is unchanged.
	var placeholders []*graph.Edge
	for e := range g.EdgesByKind(graph.EdgeCalls) {
		if e == nil || e.Meta == nil {
			continue
		}
		if v, _ := e.Meta["via"].(string); v != objectRegistryVia {
			continue
		}
		if className, _ := e.Meta["registry_value"].(string); className == "" {
			continue
		}
		placeholders = append(placeholders, e)
	}
	if len(placeholders) == 0 {
		return 0
	}

	// methodIndex: className → method-name → method nodes, built from the
	// EdgeMemberOf edges that link a method to its class.
	classByMethod := map[string]string{}
	for e := range g.EdgesByKind(graph.EdgeMemberOf) {
		if e == nil || e.From == "" || e.To == "" {
			continue
		}
		classByMethod[e.From] = registryClassName(e.To)
	}
	methodIndex := map[string]map[string][]*graph.Node{}
	for _, n := range nodesByKindsOrAll(g, graph.KindMethod, graph.KindFunction) {
		if n == nil {
			continue
		}
		cn := classByMethod[n.ID]
		if cn == "" {
			continue
		}
		if methodIndex[cn] == nil {
			methodIndex[cn] = map[string][]*graph.Node{}
		}
		methodIndex[cn][n.Name] = append(methodIndex[cn][n.Name], n)
	}
	if len(methodIndex) == 0 {
		return 0
	}

	// Index dyn_shape dispatch edges by (from, line) so a claimed registry
	// site can suppress the parallel speculative edge.
	dynBySite := map[string][]*graph.Edge{}
	for e := range g.EdgesByKind(graph.EdgeCalls) {
		if e == nil || e.Meta == nil {
			continue
		}
		if shape, _ := e.Meta["dyn_shape"].(string); shape == "" {
			continue
		}
		key := registrySiteKey(e.From, e.Line)
		dynBySite[key] = append(dynBySite[key], e)
	}

	resolved := 0
	claimed := map[string]bool{}
	var reindex []graph.EdgeReindex
	for _, e := range placeholders {
		className, _ := e.Meta["registry_value"].(string)
		method, _ := e.Meta["registry_method"].(string)
		target := pickRegistryMethod(g, e, className, method, methodIndex)

		want := "unresolved::*." + className
		if target != nil {
			want = target.ID
		}
		if e.To == want {
			if target != nil {
				resolved++
				claimed[registrySiteKey(e.From, e.Line)] = true
			}
			continue
		}
		oldTo := e.To
		e.To = want
		if target != nil {
			e.Origin = graph.OriginASTInferred
			e.Confidence = ConfidenceHeuristic
			e.ConfidenceLabel = graph.ConfidenceLabelFor(graph.EdgeCalls, ConfidenceHeuristic)
			StampSynthesized(e, SynthObjectRegistry)
			resolved++
			claimed[registrySiteKey(e.From, e.Line)] = true
		} else {
			e.Origin = graph.OriginASTInferred
			e.Confidence = 0
			e.ConfidenceLabel = ""
			UnstampSynthesized(e)
		}
		reindex = append(reindex, graph.EdgeReindex{Edge: e, OldTo: oldTo})
	}
	for site := range claimed {
		for _, de := range dynBySite[site] {
			if de.Meta == nil {
				de.Meta = map[string]any{}
			}
			de.Meta["registry_claimed"] = true
		}
	}
	if len(reindex) > 0 {
		g.ReindexEdges(reindex)
	}
	return resolved
}

// pickRegistryMethod resolves a registered class + dispatch method to the
// method node. It prefers the method invoked at the dispatch site, falling
// back to the execute/run/handle convention, with same-file-then-unique
// disambiguation. Returns nil when ambiguous.
func pickRegistryMethod(g graph.Store, call *graph.Edge, className, method string, idx map[string]map[string][]*graph.Node) *graph.Node {
	methods := idx[className]
	if methods == nil {
		return nil
	}
	order := []string{"execute", "run", "handle"}
	if method != "" {
		order = append([]string{method}, order...)
	}
	for _, m := range order {
		if t := pickStoreAction(g, call, sameBoundaryCandidates(g, call.From, methods[m])); t != nil {
			return t
		}
	}
	return nil
}

// registryClassName returns the class-name segment of a class node ID
// (filePath::ClassName → ClassName).
func registryClassName(classID string) string {
	if i := strings.LastIndex(classID, "::"); i >= 0 {
		return classID[i+2:]
	}
	return classID
}

// registrySiteKey identifies a dispatch site by its caller + line.
func registrySiteKey(from string, line int) string {
	return from + "\x00" + strconv.Itoa(line)
}
