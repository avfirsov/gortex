package resolver

import (
	"strings"

	"github.com/zzet/gortex/internal/graph"
)

// temporalStubPrefix is the placeholder namespace the Go extractor
// emits for a Temporal workflow → activity (or workflow → child
// workflow) dispatch it can't land locally
// (`unresolved::temporal::<kind>::<name>`).
const temporalStubPrefix = unresolvedPrefix + "temporal::"

// temporalEnvDefaultConfidence is stamped on a stub edge whose name was
// resolved through an env-var-with-literal-default variable (the parser
// tags it `temporal_name_origin=env_default`). It sits in the
// speculative band (< 0.5) so the edge lands at the AMBIGUOUS label and,
// together with MetaSpeculative, is hidden from default queries: the
// runtime env override may name a different handler than the default.
const temporalEnvDefaultConfidence = 0.4

// temporalEnvDefaultInferredConfidence is stamped instead when the env-default
// was recognised with high confidence — a provable os.Getenv read or a helper
// name in the configured allow-list (`temporal_env_source` = "os_getenv" /
// "allowlist"). It sits in the inferred band (≥ 0.5, visible by default): we
// trust that the dispatch DOES default to this name, leaving the residual
// "runtime may override" risk to the optional LLM cleaning pass.
const temporalEnvDefaultInferredConfidence = 0.6

// Temporal annotation node IDs the Java extractor emits via
// EmitAnnotationEdge. The resolver consumes these to discover
// temporal-tagged interfaces and methods.
const (
	javaActivityIfaceAnnoID = "annotation::java::ActivityInterface"
	javaWorkflowIfaceAnnoID = "annotation::java::WorkflowInterface"
	javaActivityMethodID    = "annotation::java::ActivityMethod"
	javaWorkflowMethodID    = "annotation::java::WorkflowMethod"
	javaSignalMethodID      = "annotation::java::SignalMethod"
	javaQueryMethodID       = "annotation::java::QueryMethod"
	javaUpdateMethodID      = "annotation::java::UpdateMethod"
)

// ResolveTemporalCalls is the graph-wide materialisation pass for the
// Temporal workflow → activity dispatch layer (N35). It performs two
// complementary jobs:
//
//  1. Role tagging. Stamps `temporal_role` (one of "workflow" /
//     "activity" / "activity_interface" / "workflow_interface" /
//     "signal" / "query" / "update") on every node the SDK treats as
//     a workflow / activity. Discovery uses two signals: (a) Go
//     `worker.RegisterActivity(F)` / `RegisterWorkflow(F)` calls,
//     emitted by the Go extractor as EdgeCalls edges carrying
//     `Meta["via"]="temporal.register"` and `Meta["temporal_name"]=<F>`;
//     (b) Java `@ActivityInterface` / `@WorkflowInterface` /
//     `@SignalMethod` / `@QueryMethod` / `@UpdateMethod` annotations,
//     emitted by the Java extractor as EdgeAnnotated edges to a
//     well-known synthetic annotation node. For Java interface
//     annotations the role is propagated to every implementor's
//     matching method via EdgeImplements + name match — that gives
//     queries a flat view of "every activity method in this codebase"
//     without re-walking the interface chain.
//
//  2. Stub-call resolution. Every Go `workflow.ExecuteActivity(ctx, F,
//     ...)` call is emitted as an EdgeCalls edge to a
//     `unresolved::temporal::<kind>::<name>` placeholder carrying
//     `Meta["via"]="temporal.stub"`. This pass rewrites each such edge
//     to point at the function the worker registered under that name.
//     The Java side is already resolved by normal interface dispatch
//     (`stub.someMethod()` is a call on a `@ActivityInterface` type;
//     the existing AST resolver lands it on the interface method, and
//     EdgeImplements connects to the impl); the role tag in step 1 is
//     the only extra surface Java needs.
//
// The pass is a full recompute and idempotent: every temporal.stub
// edge's target is recomputed from its own `temporal_name` meta on
// every call, so it is incremental-safe — a reindex of either the
// workflow or the activity file leaves the meta intact and the next
// pass re-lands (or un-lands) the edge. graph.ReindexEdge keeps the
// out/in buckets consistent. An edge whose target is no longer in the
// graph is reset back to the placeholder and loses its
// resolution-tier metadata.
//
// Runs at every resolver settle point that already runs InferImplements
// (so the Java interface → impl chain has its EdgeImplements edges)
// and after ResolveGRPCStubCalls (so the two SDK passes share the
// same post-condition).
//
// Returns the number of temporal.stub edges pointing at a resolved
// handler after the pass.
func ResolveTemporalCalls(g graph.Store) int {
	if g == nil {
		return 0
	}
	// Serialise against other graph-wide passes that mutate Node.Meta
	// (markTestSymbolsAndEmitEdges, detectClonesAndEmitEdges,
	// reach.BuildIndex). stampTemporalRole below writes n.Meta on
	// existing graph nodes; without this lock a concurrent reader
	// (e.g. clone detection invoked from indexFile) trips the runtime's
	// "concurrent map read and map write" check.
	mu := g.ResolveMutex()
	mu.Lock()
	defer mu.Unlock()
	// Wrapper-following: before resolving stubs, propagate dispatch names
	// from wrapper call sites into fresh temporal.stub edges, so a
	// workflow that dispatches via a user helper (`executeActivity(ctx,
	// ao, "Charge", …)`) gets a real workflow→activity edge.
	// Run iteratively (max 3 passes) to handle depth > 1: wrapper A calls
	// wrapper B which calls ExecuteActivity. On iteration 1, B's stub is
	// emitted; on iteration 2, A's caller gets its stub from B. The loop
	// breaks early when a pass adds no new temporal.stub edges (fixed point).
	const maxWrapperIterations = 3
	for iter := 0; iter < maxWrapperIterations; iter++ {
		before := countTemporalStubEdges(g)
		resolveTemporalWrapperCalls(g)
		if countTemporalStubEdges(g) == before {
			break
		}
	}
	resolveTemporalExecutorFields(g)
	idx := buildTemporalIndex(g)
	resolved := 0
	var reindexBatch []graph.EdgeReindex
	// First sweep: collect stub edges and the From IDs we need so the
	// per-edge GetNode below collapses to one batch lookup.
	type stubEdge struct {
		edge       *graph.Edge
		kind, name string
	}
	var stubs []stubEdge
	fromIDSet := map[string]struct{}{}
	for e := range g.EdgesByKind(graph.EdgeCalls) {
		if e == nil || e.Meta == nil {
			continue
		}
		if v, _ := e.Meta["via"].(string); v != "temporal.stub" {
			continue
		}
		kind, _ := e.Meta["temporal_kind"].(string)
		name, _ := e.Meta["temporal_name"].(string)
		if kind == "" || name == "" {
			continue
		}
		stubs = append(stubs, stubEdge{edge: e, kind: kind, name: name})
		if e.From != "" {
			fromIDSet[e.From] = struct{}{}
		}
	}
	fromList := make([]string, 0, len(fromIDSet))
	for id := range fromIDSet {
		fromList = append(fromList, id)
	}
	callerNodes := g.GetNodesByIDs(fromList)

	for _, s := range stubs {
		e := s.edge
		callerRepo := ""
		if from := callerNodes[e.From]; from != nil {
			callerRepo = from.RepoPrefix
		}
		handlerID, origin, conf := idx.lookup(s.kind, s.name, callerRepo)

		// Const-named dispatch: when the name is a reference to a string
		// const (e.g. `constants.ChargeActivity`), the parser recorded
		// the const NAME; retry against the const's literal VALUE, which
		// is what the activity is actually registered under.
		if handlerID == "" {
			if v, ok := idx.lookupConstVal(s.name, callerRepo); ok && v != s.name {
				if id, o, c := idx.lookup(s.kind, v, callerRepo); id != "" {
					handlerID, origin, conf = id, o, c
					e.Meta["temporal_const_value"] = v
				}
			}
		}

		// Env-default const reference: the env-helper's default argument was a
		// constant reference (`GetEnvOrDefault(KEY, config.ACTIVITY_NAME_DEFAULT)`),
		// recorded as `temporal_default_const`. Substitute the constant's literal
		// VALUE through constVal, then resolve register-confirmed or by
		// convention. The env-default tier override below keeps the edge at the
		// const_ref tier (inferred, visible) regardless of how it resolved.
		if handlerID == "" {
			if cn, _ := e.Meta["temporal_default_const"].(string); cn != "" {
				if v, ok := idx.lookupConstVal(cn, callerRepo); ok && v != "" {
					if id, o, c := idx.lookup(s.kind, v, callerRepo); id != "" {
						handlerID, origin, conf = id, o, c
						e.Meta["temporal_const_value"] = v
					} else if id, mismatch := idx.lookupConvention(s.kind, v, callerRepo); id != "" {
						handlerID = id
						if mismatch {
							origin, conf = graph.OriginSpeculative, 0.45
							e.Meta["temporal_resolution_via"] = "convention_mismatch"
						} else {
							origin, conf = graph.OriginASTInferred, 0.6
							e.Meta["temporal_resolution_via"] = "convention"
						}
						e.Meta["temporal_const_value"] = v
					}
				}
			}
		}

		// Func-returning-literal dispatch (G2): the stub carries
		// `temporal_name_func=<func>` when the dispatch arg was a
		// call_expression (`ExecuteActivity(ctx, pkg.GetFooName(), …)`).
		// Resolve funcName → constVal literal → handler.
		if handlerID == "" {
			if fn, _ := e.Meta["temporal_name_func"].(string); fn != "" {
				if v, ok := idx.lookupConstVal(fn, callerRepo); ok {
					if id, o, c := idx.lookup(s.kind, v, callerRepo); id != "" {
						handlerID, origin, conf = id, o, c
						e.Meta["temporal_const_value"] = v
					}
					// Even if lookup fails, record the resolved literal for diagnostics.
					if handlerID == "" {
						if id, mismatch := idx.lookupConvention(s.kind, v, callerRepo); id != "" {
							handlerID = id
							if mismatch {
								origin, conf = graph.OriginSpeculative, 0.45
								e.Meta["temporal_resolution_via"] = "convention_mismatch"
							} else {
								origin, conf = graph.OriginASTInferred, 0.6
								e.Meta["temporal_resolution_via"] = "convention"
							}
							e.Meta["temporal_const_value"] = v
						}
					}
				}
			}
		}

		// Convention fallback: dispatch to an activity/workflow FUNCTION
		// by name when the worker registers it elsewhere (unregistered
		// here) — Pattern 2 / Stage 1.2. Try the dispatch name, then its
		// const value. Landed at the inferred tier (name convention, not
		// a register-confirmed binding).
		if handlerID == "" {
			candNames := []string{s.name}
			if v, ok := idx.lookupConstVal(s.name, callerRepo); ok && v != s.name {
				candNames = append(candNames, v)
			}
			for _, nm := range candNames {
				if id, mismatch := idx.lookupConvention(s.kind, nm, callerRepo); id != "" {
					handlerID = id
					if mismatch {
						origin, conf = graph.OriginSpeculative, 0.45
						e.Meta["temporal_resolution_via"] = "convention_mismatch"
					} else {
						origin, conf = graph.OriginASTInferred, 0.6
						e.Meta["temporal_resolution_via"] = "convention"
					}
					if nm != s.name {
						e.Meta["temporal_const_value"] = nm
					}
					break
				}
			}
		}

		// Fuzzy fallback: only when exact + const + convention all failed.
		// A single kind-suffixed function whose name contains the dispatch
		// name (or its core) is the best remaining guess; land it at the
		// speculative tier so it is hidden from default queries.
		if handlerID == "" {
			if id := idx.lookupFuzzy(s.kind, s.name, callerRepo); id != "" {
				handlerID, origin, conf = id, graph.OriginSpeculative, 0.5
				e.Meta["temporal_resolution_via"] = "fuzzy"
				e.Meta[graph.MetaSpeculative] = true
			}
		}

		// When the name came from an env-var-with-literal-default variable,
		// the value is a best-guess (the runtime env may differ from the
		// literal default). HOW it was recognised decides the tier:
		//   - "allowlist" / "os_getenv" / "const_ref": we are confident this IS
		//     an env-with-default — land at the inferred tier (0.6, visible).
		//   - "heuristic" (or unknown/legacy): a generic env-named-helper
		//     guess — land at the hidden speculative tier (0.4), where the
		//     optional LLM cleaning pass can confirm or prune it.
		envDefault := false
		envSource := ""
		if v, _ := e.Meta["temporal_name_origin"].(string); v == "env_default" {
			envDefault = true
			envSource, _ = e.Meta["temporal_env_source"].(string)
		}
		envSpeculative := envDefault && envSource != "allowlist" && envSource != "os_getenv" && envSource != "const_ref"
		if handlerID != "" && envDefault {
			if envSpeculative {
				origin = graph.OriginSpeculative
				conf = temporalEnvDefaultConfidence
			} else {
				origin = graph.OriginASTInferred
				conf = temporalEnvDefaultInferredConfidence
			}
		}

		want := handlerID
		if want == "" {
			want = temporalStubPlaceholder(s.kind, s.name)
		}
		if e.To == want {
			if handlerID != "" {
				resolved++
			}
			continue
		}

		oldTo := e.To
		e.To = want
		if handlerID != "" {
			e.Origin = origin
			e.Confidence = conf
			e.ConfidenceLabel = graph.ConfidenceLabelFor(graph.EdgeCalls, conf)
			e.Meta["temporal_resolution"] = origin
			if envSpeculative {
				e.Meta[graph.MetaSpeculative] = true
			} else if via, _ := e.Meta["temporal_resolution_via"].(string); via == "convention_mismatch" {
				e.Meta[graph.MetaSpeculative] = true
			}
			StampSynthesized(e, SynthTemporalStub)
			resolved++
		} else {
			e.Origin = ""
			e.Confidence = 0
			e.ConfidenceLabel = ""
			delete(e.Meta, "temporal_resolution")
			delete(e.Meta, graph.MetaSpeculative)
			UnstampSynthesized(e)
		}
		reindexBatch = append(reindexBatch, graph.EdgeReindex{Edge: e, OldTo: oldTo})
	}
	if len(reindexBatch) > 0 {
		g.ReindexEdges(reindexBatch)
	}
	// Link signal senders / query callers to the workflows that handle
	// them, by shared name.
	resolveTemporalSignalQueryLinks(g)
	// Link Java consumers (workflow starts / signals / queries) to the Go
	// workflows and handlers they target, by shared canonical name.
	resolveTemporalCrossLanguage(g)
	return resolved
}

// resolveTemporalSignalQueryLinks connects the consumer side of the
// signal/query namespaces to the provider side, both within Go:
//
//   - provider: an in-workflow handler declaration
//     (`workflow.GetSignalChannel(ctx, "cancel")` /
//     `SetQueryHandler(ctx, "status", …)`), emitted by the parser as a
//     via=temporal.handler edge from the workflow function, kind ∈
//     {signal, query, update}, temporal_name = the handler name.
//   - consumer: an outbound send/call
//     (`SignalExternalWorkflow` / client `SignalWorkflow` / `QueryWorkflow`),
//     emitted as via=temporal.signal-send / temporal.query-call,
//     temporal_name = the same name.
//
// For each consumer it emits an EdgeCalls edge from the sender to every
// workflow that handles that signal/query, tagged
// via=temporal.signal-link / temporal.query-link. graph.AddEdge dedupes,
// so the pass is idempotent. Cross-language (Java↔Go) linking is a
// separate pass; this one stays within the Go graph.
func resolveTemporalSignalQueryLinks(g graph.Store) {
	// providers["<kind>::<name>"] → set of handler-owning workflow IDs.
	providers := map[string][]string{}
	for e := range g.EdgesByKind(graph.EdgeCalls) {
		if e == nil || e.Meta == nil || e.From == "" {
			continue
		}
		if v, _ := e.Meta["via"].(string); v != "temporal.handler" {
			continue
		}
		kind, _ := e.Meta["temporal_kind"].(string)
		name, _ := e.Meta["temporal_name"].(string)
		if kind == "" || name == "" {
			continue
		}
		providers[kind+"::"+name] = append(providers[kind+"::"+name], e.From)
	}
	if len(providers) == 0 {
		return
	}

	type link struct {
		from, to, via, kind, name, file string
		line                            int
	}
	var out []link
	for e := range g.EdgesByKind(graph.EdgeCalls) {
		if e == nil || e.Meta == nil || e.From == "" {
			continue
		}
		via, _ := e.Meta["via"].(string)
		var kind, linkVia string
		switch via {
		case "temporal.signal-send":
			kind, linkVia = "signal", "temporal.signal-link"
		case "temporal.query-call":
			kind, linkVia = "query", "temporal.query-link"
		default:
			continue
		}
		name, _ := e.Meta["temporal_name"].(string)
		if name == "" {
			continue
		}
		for _, wid := range providers[kind+"::"+name] {
			if wid == e.From {
				continue
			}
			out = append(out, link{
				from: e.From, to: wid, via: linkVia,
				kind: kind, name: name, file: e.FilePath, line: e.Line,
			})
		}
	}
	for _, l := range out {
		g.AddEdge(&graph.Edge{
			From:     l.from,
			To:       l.to,
			Kind:     graph.EdgeCalls,
			FilePath: l.file,
			Line:     l.line,
			Origin:   graph.OriginASTInferred,
			Meta: map[string]any{
				"via":           l.via,
				"temporal_kind": l.kind,
				"temporal_name": l.name,
			},
		})
	}
}

// temporalStubPlaceholder is the canonical placeholder target for an
// unresolved Temporal stub call.
func temporalStubPlaceholder(kind, name string) string {
	return temporalStubPrefix + kind + "::" + name
}

// countTemporalStubEdges returns the number of EdgeCalls edges carrying
// Meta["via"]=="temporal.stub". Used by the iterative wrapper-following
// driver to detect fixed-point convergence.
//
// PURPOSE: fixed-point sentinel for the iterative wrapper-following loop.
// RATIONALE: graph.AddEdge dedupes by key, so a pass that adds nothing new
// leaves the count unchanged — the loop can break early.
// KEYWORDS: temporal, wrapper, convergence, fixed-point
func countTemporalStubEdges(g graph.Store) int {
	n := 0
	for e := range g.EdgesByKind(graph.EdgeCalls) {
		if e == nil || e.Meta == nil {
			continue
		}
		if v, _ := e.Meta["via"].(string); v == "temporal.stub" {
			n++
		}
	}
	return n
}

// parseJavaAnnotationName extracts the value of a `name = "..."` argument
// from a Java annotation's raw argument text (e.g.
// `@WorkflowMethod(name = "my-workflow")` → "my-workflow"). Returns ""
// when there's no `name` argument. Handles single and double quotes and
// optional whitespace around `=`; matches `name` only at a token boundary
// so it doesn't trip on another argument that merely contains "name".
func parseJavaAnnotationName(args string) string {
	for i := 0; i+4 <= len(args); i++ {
		if args[i:i+4] != "name" {
			continue
		}
		if i > 0 {
			p := args[i-1]
			if p == '_' || (p >= 'a' && p <= 'z') || (p >= 'A' && p <= 'Z') || (p >= '0' && p <= '9') {
				continue // part of a longer identifier
			}
		}
		j := i + 4
		for j < len(args) && (args[j] == ' ' || args[j] == '\t') {
			j++
		}
		if j >= len(args) || args[j] != '=' {
			continue
		}
		j++
		for j < len(args) && (args[j] == ' ' || args[j] == '\t') {
			j++
		}
		if j >= len(args) || (args[j] != '"' && args[j] != '\'') {
			continue
		}
		q := args[j]
		j++
		start := j
		for j < len(args) && args[j] != q {
			j++
		}
		if j <= len(args) && j >= start {
			return args[start:j]
		}
	}
	return ""
}

// resolveTemporalCrossLanguage links Java consumers to the Go workflows /
// handlers they target, by shared canonical name:
//
//   - a Java `@WorkflowInterface` method (role "workflow", canonical name
//     from `@WorkflowMethod(name=…)`) → the Go workflow registered /
//     named the same  → via=temporal.start-workflow.
//   - a Java `@SignalMethod(name="cancel")` → a Go workflow that serves
//     signal "cancel" (via=temporal.handler kind=signal) →
//     via=temporal.signal-link.
//   - a Java `@QueryMethod(name="status")` → a Go query handler →
//     via=temporal.query-link.
//
// All edges carry cross_language=true and are emitted at the inferred
// tier (the link is by string name across the type-system boundary).
// graph.AddEdge dedupes → idempotent.
func resolveTemporalCrossLanguage(g graph.Store) {
	// Go provider indexes, by name.
	goWorkflow := map[string][]string{}
	goSignalWf := map[string][]string{}
	goQueryWf := map[string][]string{}
	addGoWorkflow := func(n *graph.Node) {
		if n == nil || n.Language != "go" {
			return
		}
		if r, _ := n.Meta["temporal_role"].(string); r != "workflow" {
			return
		}
		name := n.Name
		if tn, _ := n.Meta["temporal_name"].(string); tn != "" {
			name = tn
		}
		goWorkflow[name] = append(goWorkflow[name], n.ID)
		if n.Name != name {
			goWorkflow[n.Name] = append(goWorkflow[n.Name], n.ID)
		}
	}
	for n := range g.NodesByKind(graph.KindFunction) {
		addGoWorkflow(n)
	}
	for n := range g.NodesByKind(graph.KindMethod) {
		addGoWorkflow(n)
	}
	for e := range g.EdgesByKind(graph.EdgeCalls) {
		if e == nil || e.Meta == nil || e.From == "" {
			continue
		}
		if v, _ := e.Meta["via"].(string); v != "temporal.handler" {
			continue
		}
		from := g.GetNode(e.From)
		if from == nil || from.Language != "go" {
			continue
		}
		kind, _ := e.Meta["temporal_kind"].(string)
		name, _ := e.Meta["temporal_name"].(string)
		if name == "" {
			continue
		}
		switch kind {
		case "signal":
			goSignalWf[name] = append(goSignalWf[name], e.From)
		case "query":
			goQueryWf[name] = append(goQueryWf[name], e.From)
		}
	}
	if len(goWorkflow) == 0 && len(goSignalWf) == 0 && len(goQueryWf) == 0 {
		return
	}

	type link struct {
		from, to, via string
	}
	var out []link
	consume := func(n *graph.Node) {
		if n == nil || n.Language != "java" {
			return
		}
		role, _ := n.Meta["temporal_role"].(string)
		name, _ := n.Meta["temporal_name"].(string)
		if name == "" {
			name = n.Name
		}
		var targets []string
		var via string
		switch role {
		case "workflow":
			targets, via = goWorkflow[name], "temporal.start-workflow"
		case "signal":
			targets, via = goSignalWf[name], "temporal.signal-link"
		case "query":
			targets, via = goQueryWf[name], "temporal.query-link"
		default:
			return
		}
		for _, to := range targets {
			if to != n.ID {
				out = append(out, link{from: n.ID, to: to, via: via})
			}
		}
	}
	for n := range g.NodesByKind(graph.KindMethod) {
		consume(n)
	}
	for n := range g.NodesByKind(graph.KindFunction) {
		consume(n)
	}
	for _, l := range out {
		g.AddEdge(&graph.Edge{
			From:   l.from,
			To:     l.to,
			Kind:   graph.EdgeCalls,
			Origin: graph.OriginASTInferred,
			Meta: map[string]any{
				"via":            l.via,
				"cross_language": true,
			},
		})
	}
}

// resolveTemporalWrapperCalls implements one level of Temporal dispatch
// wrapper-following. A "wrapper" is a function whose body dispatches with
// one of its own parameters as the name (`func exec(ctx, name, …) {
// workflow.ExecuteActivity(ctx, name, …) }`); the parser flags that
// internal stub with Meta["temporal_name_param"]=<param>. This pass finds
// each wrapper's call sites, reads the argument at the wrapper's
// name-parameter position (recorded by the parser as Meta["arg_names"]),
// and emits a fresh temporal.stub edge from the caller carrying that name
// — a string-literal value, or a const NAME that the main pass then
// resolves through the const-value index. graph.AddEdge dedupes, so the
// pass is idempotent.
func resolveTemporalWrapperCalls(g graph.Store) {
	type wrapper struct {
		id, kind, name string
		pos            int
	}
	// Discover wrappers and resolve each one's name-parameter position.
	byID := map[string]wrapper{}
	byName := map[string][]wrapper{}
	for e := range g.EdgesByKind(graph.EdgeCalls) {
		if e == nil || e.Meta == nil || e.From == "" {
			continue
		}
		if v, _ := e.Meta["via"].(string); v != "temporal.stub" {
			continue
		}
		param, _ := e.Meta["temporal_name_param"].(string)
		kind, _ := e.Meta["temporal_kind"].(string)
		if param == "" || kind == "" {
			continue
		}
		if _, seen := byID[e.From]; seen {
			continue
		}
		pn := g.GetNode(e.From + "#param:" + param)
		if pn == nil {
			continue
		}
		pos, ok := metaIntValue(pn.Meta["position"])
		if !ok {
			continue
		}
		wname := ""
		if wnode := g.GetNode(e.From); wnode != nil {
			wname = wnode.Name
		}
		w := wrapper{id: e.From, kind: kind, name: wname, pos: pos}
		byID[e.From] = w
		if wname != "" {
			byName[wname] = append(byName[wname], w)
		}
	}
	if len(byID) == 0 {
		return
	}

	type pending struct {
		from, file, kind, name, wrapperName string
		line                                int
		// fwdParam, when non-empty, marks this emitted stub as itself a
		// name-forwarding wrapper: the caller passed its OWN parameter
		// (named fwdParam) into the inner wrapper's name position, so the
		// caller is a transitive wrapper the NEXT iteration must discover.
		// The stub then carries temporal_name_param=fwdParam (the depth>1
		// hook), enabling iterative resolution.
		fwdParam string
	}
	var out []pending
	emit := func(w wrapper, ce *graph.Edge) {
		if ce.From == w.id {
			return
		}
		name := argNameAt(ce, w.pos)
		if name == "" {
			return
		}
		// Depth>1 propagation: if the forwarded argument is itself a
		// parameter of the caller (a `<caller>#param:<name>` node with a
		// position exists), the caller merely passes a name THROUGH — it is
		// a transitive wrapper. Emit a temporal_name_param stub so the next
		// iteration discovers the caller as a wrapper and reaches its own
		// callers. Otherwise the argument is a literal / const NAME the main
		// resolver lands directly, and no further hop is needed.
		fwd := ""
		if pn := g.GetNode(ce.From + "#param:" + name); pn != nil && pn.Kind == graph.KindParam {
			if _, ok := metaIntValue(pn.Meta["position"]); ok {
				fwd = name
			}
		}
		out = append(out, pending{
			from: ce.From, file: ce.FilePath, line: ce.Line,
			kind: w.kind, name: name, wrapperName: w.name, fwdParam: fwd,
		})
	}
	// Match each call that carries arg_names to a wrapper, by resolved
	// node (same-repo) or by callee name (when cross-package / cross-repo
	// resolution couldn't land the edge on the wrapper's node).
	for ce := range g.EdgesByKind(graph.EdgeCalls) {
		if ce == nil || ce.From == "" || ce.Meta == nil {
			continue
		}
		if _, ok := ce.Meta["arg_names"]; !ok {
			continue
		}
		if w, ok := byID[ce.To]; ok {
			emit(w, ce)
			continue
		}
		callee, _ := ce.Meta["callee"].(string)
		if callee == "" {
			continue
		}
		for _, w := range byName[callee] {
			emit(w, ce)
		}
	}

	for _, p := range out {
		// Idempotence guard: skip if an equivalent stub already exists on
		// this caller. graph.AddEdge dedupes by key, but when fwdParam is
		// set the stub may have been emitted by a prior iteration with
		// temporal_name_param already set — avoid replacing the pointer.
		if temporalWrapperStubExists(g, p.from, p.kind, p.name) {
			continue
		}
		meta := map[string]any{
			"via":                  "temporal.stub",
			"temporal_kind":        p.kind,
			"temporal_name":        p.name,
			"temporal_via_wrapper": p.wrapperName,
		}
		// Transitive wrapper: stamp temporal_name_param so the next
		// iteration discovers p.from as a wrapper and propagates through
		// to its own callers (depth > 1).
		if p.fwdParam != "" {
			meta["temporal_name_param"] = p.fwdParam
		}
		g.AddEdge(&graph.Edge{
			From:     p.from,
			To:       temporalStubPlaceholder(p.kind, p.name),
			Kind:     graph.EdgeCalls,
			FilePath: p.file,
			Line:     p.line,
			Meta:     meta,
		})
	}
}

// temporalWrapperStubExists reports whether `from` already carries a
// via=temporal.stub out-edge for (kind, name). Used by the idempotence
// guard inside resolveTemporalWrapperCalls to prevent re-minting stubs
// across iterative passes.
//
// PURPOSE: pointer-stability guard for the iterative wrapper-following loop.
// RATIONALE: graph.AddEdge deduplicates by edge key; re-adding replaces the
// stored *Edge pointer, which breaks any retargeted in-edge the main resolver
// already resolved. Skipping re-emission keeps both out-edge and in-edge
// buckets pointing at the same *Edge.
// KEYWORDS: temporal, wrapper, idempotence, dedup
func temporalWrapperStubExists(g graph.Store, from, kind, name string) bool {
	for _, e := range g.GetOutEdges(from) {
		if e == nil || e.Meta == nil {
			continue
		}
		if v, _ := e.Meta["via"].(string); v != "temporal.stub" {
			continue
		}
		ek, _ := e.Meta["temporal_kind"].(string)
		en, _ := e.Meta["temporal_name"].(string)
		if ek == kind && en == name {
			return true
		}
	}
	return false
}

// resolveTemporalExecutorFields implements the Temporal step/executor
// pattern: a struct whose method dispatches an activity via one of its
// own fields (`func (e *ActivityExecutor) Execute(ctx) { ExecuteActivity(
// ctx, e.ActivityName, …) }`), constructed with the field set to a string
// (`ActivityExecutor{ActivityName: "Charge"}`). The parser flags the
// dispatch stub with temporal_name_field + temporal_recv_type, and emits
// a via=temporal.executor-field marker at each construction site carrying
// (executor_type, executor_field, executor_value). This pass joins them
// by (type, field) and emits a fresh temporal.stub from the constructor
// to the named activity. graph.AddEdge dedupes → idempotent.
func resolveTemporalExecutorFields(g graph.Store) {
	// dispatchKind["<type>::<field>"] = activity/workflow kind.
	dispatchKind := map[string]string{}
	for e := range g.EdgesByKind(graph.EdgeCalls) {
		if e == nil || e.Meta == nil {
			continue
		}
		if v, _ := e.Meta["via"].(string); v != "temporal.stub" {
			continue
		}
		field, _ := e.Meta["temporal_name_field"].(string)
		rtype, _ := e.Meta["temporal_recv_type"].(string)
		kind, _ := e.Meta["temporal_kind"].(string)
		if field == "" || rtype == "" || kind == "" {
			continue
		}
		dispatchKind[rtype+"::"+field] = kind
	}
	if len(dispatchKind) == 0 {
		return
	}

	type pending struct {
		from, file, kind, name string
		line                   int
	}
	var out []pending
	for e := range g.EdgesByKind(graph.EdgeCalls) {
		if e == nil || e.Meta == nil || e.From == "" {
			continue
		}
		if v, _ := e.Meta["via"].(string); v != "temporal.executor-field" {
			continue
		}
		rtype, _ := e.Meta["executor_type"].(string)
		field, _ := e.Meta["executor_field"].(string)
		value, _ := e.Meta["executor_value"].(string)
		if rtype == "" || field == "" || value == "" {
			continue
		}
		kind, ok := dispatchKind[rtype+"::"+field]
		if !ok {
			continue
		}
		out = append(out, pending{from: e.From, file: e.FilePath, line: e.Line, kind: kind, name: value})
	}
	for _, p := range out {
		g.AddEdge(&graph.Edge{
			From:     p.from,
			To:       temporalStubPlaceholder(p.kind, p.name),
			Kind:     graph.EdgeCalls,
			FilePath: p.file,
			Line:     p.line,
			Meta: map[string]any{
				"via":                   "temporal.stub",
				"temporal_kind":         p.kind,
				"temporal_name":         p.name,
				"temporal_via_executor": true,
			},
		})
	}
}

// metaIntValue reads an int stored in Node.Meta, tolerating the float64 /
// int64 forms a JSON-backed store may round-trip through.
func metaIntValue(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}

// argNameAt reads the reduced positional argument name the parser
// recorded on a call edge (Meta["arg_names"]), tolerating both []string
// and the []any form a JSON-backed store round-trips through.
func argNameAt(e *graph.Edge, pos int) string {
	if e == nil || e.Meta == nil || pos < 0 {
		return ""
	}
	switch a := e.Meta["arg_names"].(type) {
	case []string:
		if pos < len(a) {
			return a[pos]
		}
	case []any:
		if pos < len(a) {
			if s, ok := a[pos].(string); ok {
				return s
			}
		}
	}
	return ""
}

// temporalIndex maps (kind, name) to candidate handler nodes plus the
// origin / confidence tier the resolver should stamp on the rewritten
// edge.
type temporalIndex struct {
	// byKindName maps "<kind>::<name>" → handler candidate nodes.
	byKindName map[string][]*graph.Node
	// constVal maps a string-const NAME → its literal VALUE, used to
	// resolve a const-named dispatch (`ExecuteActivity(ctx,
	// constants.ChargeActivity, …)`) to the activity registered under
	// the const's value. A name that resolves to two different values
	// across the workspace is ambiguous and omitted.
	constVal map[string]string
	// constValByRepo maps "repoPrefix::constName" → literal value, the repo-scoped
	// companion to constVal. Where constVal drops a name with ≥2 distinct values
	// across the workspace (ambiguity abstention), constValByRepo preserves each
	// repo's value under a repo-prefixed key. The lookup path tries repo-affinity
	// first (dispatch from repo X → constValByRepo["X::name"]), then falls back
	// to the global constVal.
	constValByRepo map[string]string
	// funcByName indexes Go functions / methods whose name follows the
	// activity / workflow naming convention (suffix "Activity" /
	// "Workflow"), keyed by bare name. Used as a last-resort, lower-
	// confidence resolution for dispatch to an UNREGISTERED activity —
	// the common case where activity repos hold the functions but the
	// `worker.Register*` calls live in a separate worker-runner (so the
	// register-based byKindName index never sees them). Pattern 2's
	// two-part name resolves here once F1/F2 reduce it to the func name.
	funcByName map[string][]*graph.Node
}

// lookupConstVal resolves a const name to its literal value, preferring the
// repo-scoped entry (when the caller's repo defines a different value than
// the global ambiguous drop would suggest) and falling back to the global
// entry. Returns ("", false) when neither scope has the name.
func (idx *temporalIndex) lookupConstVal(name, callerRepo string) (string, bool) {
	if callerRepo != "" {
		if v, ok := idx.constValByRepo[callerRepo+"::"+name]; ok {
			return v, true
		}
	}
	v, ok := idx.constVal[name]
	return v, ok
}

// isCrossRepoTestStub reports whether candidate n is a `*_test.go` node in a
// DIFFERENT repo than the dispatching caller. Such a node is, in practice, a
// test mock / fixture of the activity / workflow (a `workflow_test.go` stub),
// not the real cross-repo implementation; matching a dispatch to it mints a
// spurious edge — the one confirmed false positive in the L1 corpus audit (a
// service repo's dispatch resolving to a `*_test.go` stub in an unrelated
// workflow repo). Same-repo test files stay eligible: the overwhelmingly
// common test-workflow → test-activity edge within one package is correct.
// An empty callerRepo or candidate RepoPrefix can't establish the cross-repo
// relation, so the node is left eligible (precision over recall in reverse —
// we only suppress when we are sure both repos are known and differ).
func isCrossRepoTestStub(n *graph.Node, callerRepo string) bool {
	if n == nil || callerRepo == "" || n.RepoPrefix == "" || n.RepoPrefix == callerRepo {
		return false
	}
	return strings.HasSuffix(n.FilePath, "_test.go")
}

// eligibleTemporalCandidates drops cross-repo `*_test.go` stub candidates (see
// isCrossRepoTestStub) from a candidate list, returning the input unchanged
// when nothing is suppressed.
func eligibleTemporalCandidates(cands []*graph.Node, callerRepo string) []*graph.Node {
	if callerRepo == "" {
		return cands
	}
	var out []*graph.Node
	suppressed := false
	for _, n := range cands {
		if isCrossRepoTestStub(n, callerRepo) {
			suppressed = true
			continue
		}
		out = append(out, n)
	}
	if !suppressed {
		return cands
	}
	return out
}

func (idx *temporalIndex) lookup(kind, name, callerRepo string) (id, origin string, confidence float64) {
	cands := eligibleTemporalCandidates(idx.byKindName[kind+"::"+name], callerRepo)
	if len(cands) == 0 {
		return "", "", 0
	}
	// Prefer same-repo, then unique overall.
	var sameRepo []*graph.Node
	for _, n := range cands {
		if callerRepo != "" && n.RepoPrefix == callerRepo {
			sameRepo = append(sameRepo, n)
		}
	}
	if len(sameRepo) == 1 {
		return sameRepo[0].ID, graph.OriginASTResolved, 0.9
	}
	if len(sameRepo) == 0 && len(cands) == 1 {
		return cands[0].ID, graph.OriginASTResolved, 0.9
	}
	return "", "", 0
}

// lookupConvention resolves a dispatch name to a convention-named Go
// function (suffix "Activity" / "Workflow" matching the kind) when no
// registered handler matched — the unregistered-activity case (Pattern 2
// / Stage 1.2). Returns ("", false) when there's no candidate. The
// second return value indicates whether the single candidate's signature
// mismatches the dispatch kind (e.g., kind="activity" but candidate
// takes workflow.Context — a workflow wrapper, not a real activity). The
// caller uses the mismatch flag to lower the confidence tier.
func (idx *temporalIndex) lookupConvention(kind, name, callerRepo string) (string, bool) {
	suffix := "Activity"
	if kind == "workflow" {
		suffix = "Workflow"
	}
	// core is the dispatch name with any redundant kind-suffix trimmed: a
	// dispatch of bare "Charge" and a dispatch of the already-suffixed
	// "ChargeActivity" both reduce to the core "Charge", which the matching
	// activity FUNCTION ("ChargeActivity", "MyChargeActivity", …) contains.
	// Trimming keeps the suffixed and bare forms backward-compatible: for
	// name="ChargeActivity", core="Charge" and "ChargeActivity" both
	// HasSuffix and Contains(core), so the original suffix-exact match is
	// preserved while bare-name dispatch now resolves too.
	core := strings.TrimSuffix(name, suffix)
	if core == "" {
		return "", false
	}
	// Iterate the whole convention index (not an exact-name key): the bare
	// dispatch name is, by construction, NOT the func name, so an exact-key
	// lookup never matched the cross-repo / unregistered FUNCTION it names.
	var filtered, sameRepo []*graph.Node
	for fn, cands := range idx.funcByName {
		if !strings.HasSuffix(fn, suffix) || !strings.Contains(fn, core) {
			continue
		}
		for _, n := range cands {
			if isCrossRepoTestStub(n, callerRepo) {
				continue
			}
			filtered = append(filtered, n)
			if callerRepo != "" && n.RepoPrefix == callerRepo {
				sameRepo = append(sameRepo, n)
			}
		}
	}
	if len(sameRepo) == 1 {
		mismatch := signatureMismatchesKind(sameRepo[0], kind)
		return sameRepo[0].ID, mismatch
	}
	if len(filtered) == 1 {
		mismatch := signatureMismatchesKind(filtered[0], kind)
		return filtered[0].ID, mismatch
	}
	// Tiebreaker: when multiple convention candidates share the same
	// name (e.g., a real activity `func FooActivity(context.Context, …)`
	// and a workflow wrapper `func FooActivity(workflow.Context, …)`),
	// prefer the one whose signature matches the dispatch kind:
	//   kind="activity"  → prefer context.Context  (real activity)
	//   kind="workflow"  → prefer workflow.Context  (real workflow)
	// The signature meta is set by the Go extractor on every function /
	// method node. This is a Temporal Go SDK convention: activities
	// accept context.Context, workflows accept workflow.Context.
	if len(filtered) > 1 {
		if preferred := preferBySignatureKind(filtered, kind); preferred != "" {
			return preferred, false
		}
	}
	if len(sameRepo) > 1 {
		if preferred := preferBySignatureKind(sameRepo, kind); preferred != "" {
			return preferred, false
		}
	}
	return "", false
}

// signatureMismatchesKind reports whether a convention candidate's function
// signature contradicts the dispatch kind. In Temporal Go SDK:
//   - Activities accept context.Context
//   - Workflows accept workflow.Context
//
// A kind="activity" dispatch that lands on a function taking
// workflow.Context is likely a workflow wrapper, not the real activity.
// The mismatch flag lets the caller lower the confidence tier instead of
// rejecting the match outright — the wrapper is still a meaningful signal.
func signatureMismatchesKind(n *graph.Node, kind string) bool {
	if n == nil {
		return false
	}
	sig, _ := n.Meta["signature"].(string)
	if sig == "" {
		return false
	}
	want := "context.Context"
	dontWant := "workflow.Context"
	if kind == "workflow" {
		want, dontWant = "workflow.Context", "context.Context"
	}
	return strings.Contains(sig, dontWant) && !strings.Contains(sig, want)
}

// preferBySignatureKind applies the context.Context / workflow.Context
// tiebreaker to a list of convention candidates. Returns the single
// candidate whose Meta["signature"] contains the preferred first-param
// type for the given kind, or "" if zero or 2+ candidates match.
func preferBySignatureKind(cands []*graph.Node, kind string) string {
	want := "context.Context"
	if kind == "workflow" {
		want = "workflow.Context"
	}
	var matching []*graph.Node
	for _, n := range cands {
		sig, _ := n.Meta["signature"].(string)
		if sig == "" {
			continue
		}
		// Check if the signature's first parameter is the preferred type.
		// Go signatures look like: "func FooActivity(ctx context.Context, …)"
		// or "func FooActivity(ctx workflow.Context, …)".
		if strings.Contains(sig, want) {
			matching = append(matching, n)
		}
	}
	if len(matching) == 1 {
		return matching[0].ID
	}
	return ""
}

// lookupFuzzy is the conservative last-resort fallback, fired only when both
// the exact (register-confirmed) lookup and the convention lookup have
// failed. It re-scans the convention index for the single kind-suffixed
// FUNCTION whose name contains the raw dispatch name (or its kind-trimmed
// core) — a looser substring match than convention's suffix-core shape. To
// keep precision high it resolves ONLY when EXACTLY ONE such candidate
// exists across the workspace; any ambiguity abstains. The caller stamps the
// result at the speculative tier (confidence ≤ 0.5, MetaSpeculative) so the
// best-guess edge is hidden from default queries.
func (idx *temporalIndex) lookupFuzzy(kind, name, callerRepo string) string {
	suffix := "Activity"
	if kind == "workflow" {
		suffix = "Workflow"
	}
	core := strings.TrimSuffix(name, suffix)
	if core == "" {
		return ""
	}
	// Prefer the stricter raw-name containment; only widen to the trimmed
	// core when the raw name matches nothing. This keeps the lone-candidate
	// guarantee meaningful: a tighter needle is tried before a looser one.
	collect := func(needle string) []*graph.Node {
		var out []*graph.Node
		for fn, cands := range idx.funcByName {
			if !strings.HasSuffix(fn, suffix) {
				continue
			}
			if !strings.Contains(fn, needle) {
				continue
			}
			for _, n := range cands {
				if isCrossRepoTestStub(n, callerRepo) {
					continue
				}
				out = append(out, n)
			}
		}
		return out
	}
	matched := collect(name)
	if len(matched) == 0 && core != name {
		matched = collect(core)
	}
	if len(matched) == 1 {
		return matched[0].ID
	}
	return ""
}

// buildTemporalIndex walks the graph once and (a) stamps temporal_role
// on every node identifiable as a Temporal workflow / activity via
// either Go `worker.Register*` calls or Java `@ActivityInterface` /
// `@WorkflowInterface` annotations (propagated to interface
// implementors), and (b) returns a name index the stub-call resolver
// consults.
func buildTemporalIndex(g graph.Store) *temporalIndex {
	idx := &temporalIndex{byKindName: map[string][]*graph.Node{}, constVal: map[string]string{}, constValByRepo: map[string]string{}, funcByName: map[string][]*graph.Node{}}

	// Convention index: Go functions / methods named like activities or
	// workflows (suffix "Activity" / "Workflow"), for resolving dispatch
	// to functions the worker-runner registers elsewhere (unregistered
	// here). Bounded to the convention-named set to keep it small.
	indexConventionFunc := func(n *graph.Node) {
		if n == nil || n.Language != "go" {
			return
		}
		if n.Kind != graph.KindFunction && n.Kind != graph.KindMethod {
			return
		}
		if strings.HasSuffix(n.Name, "Activity") || strings.HasSuffix(n.Name, "Workflow") {
			idx.funcByName[n.Name] = append(idx.funcByName[n.Name], n)
		}
	}
	for n := range g.NodesByKind(graph.KindFunction) {
		indexConventionFunc(n)
	}
	for n := range g.NodesByKind(graph.KindMethod) {
		indexConventionFunc(n)
	}

	// String-const value index: name → value, dropping any name that
	// maps to more than one distinct value (ambiguous). Lets the stub
	// resolver below substitute `constants.X` → "the string X holds".
	constAmbiguous := map[string]struct{}{}
	ingestConst := func(name, v, repo string) {
		if name == "" || v == "" {
			return
		}
		// Repo-scoped: always record (same const name may have different
		// values in different repos; repo-scoped key is unique by construction).
		if repo != "" {
			idx.constValByRepo[repo+"::"+name] = v
		}
		// Global: drop on ambiguity (unchanged behavior).
		if _, dropped := constAmbiguous[name]; dropped {
			return
		}
		if prev, seen := idx.constVal[name]; seen && prev != v {
			delete(idx.constVal, name)
			constAmbiguous[name] = struct{}{}
			return
		}
		idx.constVal[name] = v
	}
	for n := range g.NodesByKind(graph.KindConstant) {
		if n == nil {
			continue
		}
		if v, ok := n.Meta["value"].(string); ok {
			ingestConst(n.Name, v, n.RepoPrefix)
		}
	}
	// Java string constants are emitted as KindField (`static final String`),
	// not KindConstant; the Java extractor stamps Meta["value"] on them. Ingest
	// those into the SAME constVal index so a Java invoker const-ref dispatch
	// (`invoker.invokeAsync(Constants.X, …)`) resolves cross-language to the
	// registered Go workflow/activity. Same ambiguity rule applies — a name with
	// two distinct values across the workspace is dropped.
	for n := range g.NodesByKind(graph.KindField) {
		if n == nil || n.Language != "java" {
			continue
		}
		if v, ok := n.Meta["value"].(string); ok {
			ingestConst(n.Name, v, n.RepoPrefix)
		}
	}

	// Function-return-literal index (G2): a func whose body is a single
	// `return "<literal>"` centralises an activity / workflow name.
	// Index funcName → literal into the same constVal map (same ambiguity
	// rule as string constants: a name with two distinct literals is
	// dropped). This lets stub edges carrying `temporal_name_func=<func>`
	// resolve via funcName → literal → registered handler.
	indexFunctionReturnLiterals := func(n *graph.Node) {
		if n == nil || n.Language != "go" {
			return
		}
		if n.Kind != graph.KindFunction && n.Kind != graph.KindMethod {
			return
		}
		lit, ok := n.Meta["temporal_const_return"].(string)
		if !ok || lit == "" {
			return
		}
		ingestConst(n.Name, lit, n.RepoPrefix)
	}
	for n := range g.NodesByKind(graph.KindFunction) {
		indexFunctionReturnLiterals(n)
	}
	for n := range g.NodesByKind(graph.KindMethod) {
		indexFunctionReturnLiterals(n)
	}

	// Phase 1 — Go side. Walk `temporal.register` edges and stamp the
	// registered function's node. The "via" tag lives on EdgeCalls
	// edges, so narrow with EdgesByKind before the Meta filter.
	//
	// Collect every register edge first so we can batch-fetch every
	// caller node and resolve every Go target name in one pair of
	// round-trips, instead of N AllNodes scans + N GetNode calls.
	type goRegister struct {
		edge       *graph.Edge
		kind, name string
	}
	var goRegisters []goRegister
	registerCallerIDs := map[string]struct{}{}
	registerNames := map[string]struct{}{}
	for e := range g.EdgesByKind(graph.EdgeCalls) {
		if e == nil || e.Meta == nil {
			continue
		}
		if v, _ := e.Meta["via"].(string); v != "temporal.register" {
			continue
		}
		kind, _ := e.Meta["temporal_kind"].(string)
		name, _ := e.Meta["temporal_name"].(string)
		if kind == "" || name == "" {
			continue
		}
		goRegisters = append(goRegisters, goRegister{edge: e, kind: kind, name: name})
		if e.From != "" {
			registerCallerIDs[e.From] = struct{}{}
		}
		registerNames[name] = struct{}{}
	}
	callerList := make([]string, 0, len(registerCallerIDs))
	for id := range registerCallerIDs {
		callerList = append(callerList, id)
	}
	registerCallers := g.GetNodesByIDs(callerList)
	nameList := make([]string, 0, len(registerNames))
	for n := range registerNames {
		nameList = append(nameList, n)
	}
	candidatesByName := g.FindNodesByNames(nameList)

	for _, r := range goRegisters {
		caller := registerCallers[r.edge.From]
		if caller == nil {
			continue
		}
		target := pickGoTemporalTarget(candidatesByName[r.name], caller)
		if target == nil {
			continue
		}
		stampTemporalRole(g, target, r.kind, r.name)
		idx.byKindName[r.kind+"::"+r.name] = append(idx.byKindName[r.kind+"::"+r.name], target)
	}

	// Phase 2 — Java side. Walk `EdgeAnnotated` edges to find
	// temporal-tagged interfaces and methods. As with Phase 1, collect
	// every annotation edge and batch the From-side GetNode calls.
	type javaAnno struct {
		fromID                string
		ifaceRole, methodRole string
		annName               string // parsed @X(name="...") override
	}
	var javaAnnos []javaAnno
	annoFromIDs := map[string]struct{}{}
	for e := range g.EdgesByKind(graph.EdgeAnnotated) {
		if e == nil {
			continue
		}
		role, methodRole := temporalRoleForJavaAnnotation(e.To)
		if role == "" && methodRole == "" {
			continue
		}
		args, _ := e.Meta["args"].(string)
		javaAnnos = append(javaAnnos, javaAnno{
			fromID: e.From, ifaceRole: role, methodRole: methodRole,
			annName: parseJavaAnnotationName(args),
		})
		if e.From != "" {
			annoFromIDs[e.From] = struct{}{}
		}
	}
	annoFromList := make([]string, 0, len(annoFromIDs))
	for id := range annoFromIDs {
		annoFromList = append(annoFromList, id)
	}
	annoFromNodes := g.GetNodesByIDs(annoFromList)

	type javaIfaceTag struct {
		ifaceID string
		role    string // "activity_interface" / "workflow_interface"
	}
	var javaIfaces []javaIfaceTag
	for _, a := range javaAnnos {
		from := annoFromNodes[a.fromID]
		if from == nil {
			continue
		}
		// Method-level annotation: stamp directly. The canonical Temporal
		// name is the @X(name="…") override when present, else the bare
		// method name; index under both so a Go side that registers under
		// either matches.
		if a.methodRole != "" && (from.Kind == graph.KindMethod || from.Kind == graph.KindFunction) {
			canonical := from.Name
			if a.annName != "" {
				canonical = a.annName
			}
			stampTemporalRole(g, from, a.methodRole, canonical)
			nk := normaliseTemporalKind(a.methodRole)
			idx.byKindName[nk+"::"+canonical] = append(idx.byKindName[nk+"::"+canonical], from)
			if canonical != from.Name {
				idx.byKindName[nk+"::"+from.Name] = append(idx.byKindName[nk+"::"+from.Name], from)
			}
			continue
		}
		// Interface-level annotation: queue for the propagation pass.
		if a.ifaceRole != "" && from.Kind == graph.KindInterface {
			stampTemporalRole(g, from, a.ifaceRole, from.Name)
			javaIfaces = append(javaIfaces, javaIfaceTag{ifaceID: from.ID, role: a.ifaceRole})
		}
	}

	// Phase 3 — Java propagation. For each tagged interface, find its
	// methods (flat nodes living in the same file, within the
	// interface's line range) and stamp them. Then walk EdgeImplements
	// from each implementor and tag its same-named methods.
	//
	// Build a single Java method index up front via NodesByKind, then
	// project it into the two views the propagation needs:
	//   - methodsByFile: file path → []*method (used for interface
	//     methods, which the Java extractor emits as flat
	//     <file>::<name> nodes whose StartLine sits inside the
	//     interface's line range).
	//   - methodsByReceiver: receiver class name → []*method (used for
	//     impl-class methods, which carry Meta["receiver"]).
	// One pass beats AllNodes() per interface.
	javaMethodsByFile, javaMethodsByReceiver := buildJavaMethodViews(g, len(javaIfaces))

	// Prefetch the interface nodes + the implementing-type nodes for
	// the entire iface set so the propagation loop never issues an
	// inline GetNode.
	ifaceIDs := make([]string, 0, len(javaIfaces))
	for _, t := range javaIfaces {
		ifaceIDs = append(ifaceIDs, t.ifaceID)
	}
	ifaceNodes := g.GetNodesByIDs(ifaceIDs)
	implTypeIDSet := map[string]struct{}{}
	implIDsByIface := map[string][]string{}
	for _, t := range javaIfaces {
		for _, ie := range g.GetInEdges(t.ifaceID) {
			if ie == nil || ie.Kind != graph.EdgeImplements {
				continue
			}
			implIDsByIface[t.ifaceID] = append(implIDsByIface[t.ifaceID], ie.From)
			if ie.From != "" {
				implTypeIDSet[ie.From] = struct{}{}
			}
		}
	}
	implTypeIDList := make([]string, 0, len(implTypeIDSet))
	for id := range implTypeIDSet {
		implTypeIDList = append(implTypeIDList, id)
	}
	implTypeNodes := g.GetNodesByIDs(implTypeIDList)

	for _, t := range javaIfaces {
		methodRole := "activity"
		if t.role == "workflow_interface" {
			methodRole = "workflow"
		}
		iface := ifaceNodes[t.ifaceID]
		if iface == nil {
			continue
		}
		ifaceMethods := collectJavaInterfaceMethodsFromIndex(iface, javaMethodsByFile)
		for _, m := range ifaceMethods {
			// A method carrying its own @WorkflowMethod / @SignalMethod /
			// @QueryMethod / @UpdateMethod annotation was already stamped
			// (with its name= override) in Phase 2 — don't let the
			// interface-level role clobber a more specific method role.
			if r, _ := m.Meta["temporal_role"].(string); r != "" {
				continue
			}
			stampTemporalRole(g, m, methodRole, m.Name)
			idx.byKindName[methodRole+"::"+m.Name] = append(idx.byKindName[methodRole+"::"+m.Name], m)
		}
		// Propagate to implementing classes' methods.
		implMethodNames := map[string]struct{}{}
		for _, m := range ifaceMethods {
			implMethodNames[m.Name] = struct{}{}
		}
		for _, implTypeID := range implIDsByIface[t.ifaceID] {
			implType := implTypeNodes[implTypeID]
			if implType == nil {
				continue
			}
			for _, m := range methodsOfJavaTypeFromIndex(implType, javaMethodsByReceiver) {
				if _, ok := implMethodNames[m.Name]; !ok {
					continue
				}
				stampTemporalRole(g, m, methodRole, m.Name)
				idx.byKindName[methodRole+"::"+m.Name] = append(idx.byKindName[methodRole+"::"+m.Name], m)
			}
		}
	}

	return idx
}

// temporalRoleForJavaAnnotation maps a Java annotation node ID to a
// (interface-role, method-role) pair. Only one is non-empty per
// annotation; the caller uses whichever fits the annotated node kind.
func temporalRoleForJavaAnnotation(annoID string) (ifaceRole, methodRole string) {
	switch annoID {
	case javaActivityIfaceAnnoID:
		return "activity_interface", ""
	case javaWorkflowIfaceAnnoID:
		return "workflow_interface", ""
	case javaActivityMethodID:
		return "", "activity"
	case javaWorkflowMethodID:
		return "", "workflow"
	case javaSignalMethodID:
		return "", "signal"
	case javaQueryMethodID:
		return "", "query"
	case javaUpdateMethodID:
		return "", "update"
	}
	return "", ""
}

// normaliseTemporalKind collapses the seven role tags down to the two
// kinds that drive stub-call lookup ("activity" / "workflow"). Signal
// / query / update handlers are workflow methods, not separate kinds.
func normaliseTemporalKind(role string) string {
	switch role {
	case "workflow", "signal", "query", "update":
		return "workflow"
	default:
		return "activity"
	}
}

// stampTemporalRole writes `temporal_role` and `temporal_name` into a
// node's Meta. Idempotent: re-stamping the same role is a no-op. When
// a previously-stamped node is re-stamped with a different role the
// new role wins (the resolver runs as a full recompute, so this lets
// the latest registration take precedence).
func stampTemporalRole(g graph.Store, n *graph.Node, role, name string) {
	if n == nil || role == "" {
		return
	}
	if n.Meta == nil {
		n.Meta = map[string]any{}
	}
	n.Meta["temporal_role"] = role
	if name != "" {
		n.Meta["temporal_name"] = name
	}
	// Round-trip the stamp back through the store. On the in-memory
	// backend n is canonical so this is an idempotent re-insert; on disk
	// backends n is a per-call GetNode/AllNodes reconstruction,
	// so without the write-back temporal_role/temporal_name would be
	// discarded the moment this pass returns. ResolveTemporalCalls runs
	// from RunGlobalGraphPasses, which can execute after the bulk-load
	// buffer is flushed, so the in-place mutation is not otherwise
	// captured. Matches reach / coverage / blame / releases / churn.
	g.AddNode(n)
}

// pickGoTemporalTarget selects the Go function or method that a
// `worker.Register*(F)` call refers to from a name-matched candidate
// set. The register call lives at `caller`; the function `F` is
// either declared in the same file or imported. The search order is:
//
//  1. Same-file function whose name matches.
//  2. Same-repo function whose name matches.
//  3. Unique workspace-wide function whose name matches.
//
// Returns nil when no unambiguous match exists. The candidate list
// MUST be pre-filtered to Name == registered name (FindNodesByNames
// already does that); this helper applies the Go-kind and language
// gates plus the locality tie-break.
func pickGoTemporalTarget(candidates []*graph.Node, caller *graph.Node) *graph.Node {
	if caller == nil {
		return nil
	}
	var sameFile, sameRepo, all []*graph.Node
	for _, n := range candidates {
		if n == nil {
			continue
		}
		if n.Language != "go" {
			continue
		}
		if n.Kind != graph.KindFunction && n.Kind != graph.KindMethod {
			continue
		}
		all = append(all, n)
		if caller.RepoPrefix != "" && n.RepoPrefix == caller.RepoPrefix {
			sameRepo = append(sameRepo, n)
		}
		if n.FilePath == caller.FilePath {
			sameFile = append(sameFile, n)
		}
	}
	if len(sameFile) == 1 {
		return sameFile[0]
	}
	if len(sameRepo) == 1 {
		return sameRepo[0]
	}
	if len(all) == 1 {
		return all[0]
	}
	return nil
}

// buildJavaMethodViews materialises two indexes over every Java
// method node in the graph: methodsByFile groups nodes whose Meta has
// NO "receiver" (interface methods, per the Java extractor's
// convention); methodsByReceiver groups nodes whose Meta carries a
// non-empty receiver. One NodesByKind scan replaces the N AllNodes()
// passes the old collectJavaInterfaceMethods + methodsOfJavaType
// helpers ran inside the per-interface propagation loop.
//
// ifaceCount == 0 is a fast no-op; with no tagged interfaces the
// indexes are unused so we skip the scan.
func buildJavaMethodViews(g graph.Store, ifaceCount int) (map[string][]*graph.Node, map[string][]*graph.Node) {
	if ifaceCount == 0 {
		return nil, nil
	}
	methodsByFile := map[string][]*graph.Node{}
	methodsByReceiver := map[string][]*graph.Node{}
	for n := range g.NodesByKind(graph.KindMethod) {
		if n == nil || n.Language != "java" {
			continue
		}
		recv, _ := n.Meta["receiver"].(string)
		if recv == "" {
			methodsByFile[n.FilePath] = append(methodsByFile[n.FilePath], n)
		} else {
			methodsByReceiver[recv] = append(methodsByReceiver[recv], n)
		}
	}
	return methodsByFile, methodsByReceiver
}

// collectJavaInterfaceMethodsFromIndex returns the interface's method
// nodes — flat KindMethod nodes in the interface's file whose
// StartLine sits inside the interface's line range. Consumes the
// methodsByFile view built by buildJavaMethodViews so the scan is
// O(methods in this file) rather than O(every node).
func collectJavaInterfaceMethodsFromIndex(iface *graph.Node, methodsByFile map[string][]*graph.Node) []*graph.Node {
	if iface == nil {
		return nil
	}
	var out []*graph.Node
	for _, n := range methodsByFile[iface.FilePath] {
		if n.StartLine < iface.StartLine || (iface.EndLine > 0 && n.StartLine > iface.EndLine) {
			continue
		}
		out = append(out, n)
	}
	return out
}

// methodsOfJavaTypeFromIndex returns the method nodes whose
// Meta["receiver"] matches the type's name (or the receiver-suffix
// shape on the class node's ID). Consumes the methodsByReceiver view
// built by buildJavaMethodViews so the scan is O(methods of this
// receiver) rather than O(every node).
func methodsOfJavaTypeFromIndex(t *graph.Node, methodsByReceiver map[string][]*graph.Node) []*graph.Node {
	if t == nil {
		return nil
	}
	out := methodsByReceiver[t.Name]
	// Honour the legacy id-suffix tie-break: a class node's id is
	// `<filePath>::<ClassName>`; a method whose receiver matches that
	// trailing component is still a member even when the receiver
	// Meta carries a fully-qualified name.
	for recv, candidates := range methodsByReceiver {
		if recv == t.Name {
			continue
		}
		if !strings.HasSuffix(t.ID, "::"+recv) {
			continue
		}
		out = append(out, candidates...)
	}
	return out
}
