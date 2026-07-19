package resolver

import "github.com/zzet/gortex/internal/graph"

// languageFamily maps a language to the family within which cross-language
// symbol resolution is legitimate (a `@model Foo` / `<Counter/>` reference may
// bind across languages of the same family). "" means the language belongs to
// no multi-language family, so any cross-language bind for it is coincidental.
func languageFamily(lang string) string {
	switch lang {
	case "java", "kotlin", "scala":
		return "jvm"
	case "swift", "objc", "objective-c", "objectivec":
		return "apple"
	case "typescript", "ts", "tsx", "javascript", "js", "jsx":
		return "web"
	case "c", "cpp", "c++", "cxx":
		return "c"
	case "csharp", "c#", "fsharp", "f#", "razor":
		return "dotnet"
	}
	return ""
}

// sameLanguageFamily reports whether a and b are the same language or belong to
// the same multi-language family (so a within-family cross-language bind is
// permitted): csharp↔razor, ts↔tsx, java↔kotlin, swift↔objc.
func sameLanguageFamily(a, b string) bool {
	if a == b {
		return a != ""
	}
	fa := languageFamily(a)
	return fa != "" && fa == languageFamily(b)
}

// frameworkBridgeSynths are the synthesizers whose entire purpose is to bridge
// two language families (JS→native, Swift→ObjC, KMP expect/actual). Their
// edges are exempt from the cross-family gate.
var frameworkBridgeSynths = map[string]bool{
	SynthSwiftObjC:       true,
	SynthReactNative:     true,
	SynthReactNativePair: true,
	SynthExpoModules:     true,
	SynthFabric:          true,
	SynthKMPExpectActual: true,
}

// gateFrameworkResult reports whether a framework-synthesized reference/import
// result should be dropped: it crosses two known, different language families
// (a coincidental PascalCase collision) and was not produced by a bridge
// synthesizer. An unknown family on either side, the same family, or a bridge
// synthesizer all permit the result.
func gateFrameworkResult(synth, fromLang, toLang string) bool {
	if frameworkBridgeSynths[synth] {
		return false
	}
	fa, fb := languageFamily(fromLang), languageFamily(toLang)
	if fa == "" || fb == "" {
		return false
	}
	return fa != fb
}

// applyFrameworkFamilyGate drops framework-synthesized reference / import edges
// that cross two known, different language families (e.g. a Razor reference
// that coincidentally bound a TypeScript component), keeping bridge-synthesizer
// edges and call/config edges. Returns the number of edges dropped.
func applyFrameworkFamilyGate(g graph.Store) int {
	return applyFrameworkFamilyGateScoped(g, nil)
}

// applyFrameworkFamilyGateScoped is the partial-index form. Only edges owned
// by changed repositories can have been newly synthesized in the scoped settle
// window; incoming edges to replaced targets are removed by file eviction.
// A nil scope preserves the full/cold whole-graph reconciliation.
func applyFrameworkFamilyGateScoped(g graph.Store, scope map[string]bool) int {
	if g == nil {
		return 0
	}
	type cand struct {
		edge  *graph.Edge
		synth string
	}
	var cands []cand
	endpointIDs := map[string]struct{}{}
	for _, e := range frameworkRepoEdges(g, scope, graph.EdgeReferences, graph.EdgeImports) {
		if e == nil || e.Meta == nil {
			continue
		}
		synth, _ := e.Meta[MetaSynthesizedBy].(string)
		if synth == "" || frameworkBridgeSynths[synth] {
			continue
		}
		cands = append(cands, cand{edge: e, synth: synth})
		endpointIDs[e.From] = struct{}{}
		endpointIDs[e.To] = struct{}{}
	}
	if len(cands) == 0 {
		return 0
	}
	ids := make([]string, 0, len(endpointIDs))
	for id := range endpointIDs {
		ids = append(ids, id)
	}
	nodes := g.GetNodesByIDs(ids)
	langOf := func(id string) string {
		if n := nodes[id]; n != nil {
			return n.Language
		}
		return ""
	}
	toDrop := make([]*graph.Edge, 0, len(cands))
	for _, c := range cands {
		if gateFrameworkResult(c.synth, langOf(c.edge.From), langOf(c.edge.To)) {
			toDrop = append(toDrop, c.edge)
		}
	}
	return graph.RemoveEdgesExact(g, toDrop)
}
