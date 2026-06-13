package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

// temporalTestGraph builds the minimal shape ResolveTemporalCalls
// consumes: a workflow function with a temporal.stub call edge, plus
// either a Go register-call edge or a Java @ActivityInterface +
// EdgeImplements chain that names the activity.
type temporalTestGraph struct {
	g graph.Store
}

func newTemporalTestGraph() *temporalTestGraph { return &temporalTestGraph{g: graph.New()} }

// addGoFunc adds a Go function or method node.
func (b *temporalTestGraph) addGoFunc(id, name, filePath, repo string) *graph.Node {
	n := &graph.Node{
		ID: id, Kind: graph.KindFunction, Name: name,
		FilePath: filePath, RepoPrefix: repo, Language: "go",
	}
	b.g.AddNode(n)
	return n
}

// addStubCall adds a Temporal stub-call placeholder edge from caller.
func (b *temporalTestGraph) addStubCall(callerID, kind, name, filePath string) *graph.Edge {
	e := &graph.Edge{
		From: callerID, To: temporalStubPlaceholder(kind, name),
		Kind: graph.EdgeCalls, FilePath: filePath, Line: 10,
		Meta: map[string]any{
			"via":           "temporal.stub",
			"temporal_kind": kind,
			"temporal_name": name,
		},
	}
	b.g.AddEdge(e)
	return e
}

// addStubCallEnvDefault adds a Temporal stub-call edge whose name was
// resolved from an env-var-with-literal-default variable
// (temporal_name_origin=env_default). The resolver must still land it on
// the registered handler but at the speculative tier (the runtime env
// override may differ from the default).
func (b *temporalTestGraph) addStubCallEnvDefault(callerID, kind, name, filePath string) *graph.Edge {
	e := b.addStubCall(callerID, kind, name, filePath)
	e.Meta["temporal_name_origin"] = "env_default"
	return e
}

// addStubCallNameFunc adds a Temporal stub-call edge whose dispatch name
// is supplied by a const-returning function call
// (`ExecuteActivity(ctx, GetChargeName(), …)`). The parser records the
// callee func name under temporal_name_func and leaves temporal_name as a
// non-resolving placeholder.
func (b *temporalTestGraph) addStubCallNameFunc(callerID, kind, funcName, filePath string) *graph.Edge {
	e := &graph.Edge{
		From: callerID, To: temporalStubPlaceholder(kind, funcName),
		Kind: graph.EdgeCalls, FilePath: filePath, Line: 11,
		Meta: map[string]any{
			"via":                "temporal.stub",
			"temporal_kind":      kind,
			"temporal_name":      funcName,
			"temporal_name_func": funcName,
		},
	}
	b.g.AddEdge(e)
	return e
}

// addGoConstReturnFunc adds a Go function node whose body is a single
// `return "<literal>"`, stamped temporal_const_return=<literal> by the
// extractor.
func (b *temporalTestGraph) addGoConstReturnFunc(id, name, filePath, repo, literal string) *graph.Node {
	n := b.addGoFunc(id, name, filePath, repo)
	if n.Meta == nil {
		n.Meta = map[string]any{}
	}
	n.Meta["temporal_const_return"] = literal
	b.g.AddNode(n)
	return n
}

// addGoRegister adds a Go `worker.RegisterActivity(F)` edge: an
// EdgeCalls edge from the worker-setup function to a placeholder,
// carrying the temporal.register meta the resolver consumes.
func (b *temporalTestGraph) addGoRegister(callerID, kind, name, filePath string) *graph.Edge {
	e := &graph.Edge{
		From: callerID, To: "unresolved::extern::go.temporal.io/sdk/worker::Register" + capitalise(kind),
		Kind: graph.EdgeCalls, FilePath: filePath, Line: 5,
		Meta: map[string]any{
			"via":           "temporal.register",
			"temporal_kind": kind,
			"temporal_name": name,
		},
	}
	b.g.AddEdge(e)
	return e
}

// addJavaInterface adds an interface node tagged with @ActivityInterface
// (or @WorkflowInterface) plus its method nodes (flat, no receiver) and
// the EdgeAnnotated edge to the annotation node the Java extractor
// would emit.
func (b *temporalTestGraph) addJavaInterface(ifaceID, name, filePath string, annoID string, methods ...string) (ifaceNode *graph.Node, methodNodes map[string]*graph.Node) {
	ifaceNode = &graph.Node{
		ID: ifaceID, Kind: graph.KindInterface, Name: name,
		FilePath: filePath, Language: "java",
		StartLine: 10, EndLine: 30,
	}
	b.g.AddNode(ifaceNode)

	annoNode := b.g.GetNode(annoID)
	if annoNode == nil {
		b.g.AddNode(&graph.Node{
			ID: annoID, Kind: graph.KindType, Name: lastSeg(annoID),
			FilePath: filePath, Language: "java",
			Meta: map[string]any{"kind": "annotation", "synthetic": true},
		})
	}
	b.g.AddEdge(&graph.Edge{From: ifaceID, To: annoID, Kind: graph.EdgeAnnotated, FilePath: filePath, Line: 9})

	methodNodes = map[string]*graph.Node{}
	for i, m := range methods {
		mid := filePath + "::" + m
		mn := &graph.Node{
			ID: mid, Kind: graph.KindMethod, Name: m,
			FilePath: filePath, Language: "java",
			StartLine: 11 + i, EndLine: 11 + i,
		}
		b.g.AddNode(mn)
		methodNodes[m] = mn
	}
	return ifaceNode, methodNodes
}

// addJavaImpl adds a Java implementation class with the named methods
// and the EdgeImplements edge from class → interface.
func (b *temporalTestGraph) addJavaImpl(classID, name, filePath, ifaceID string, methods ...string) (classNode *graph.Node, methodNodes map[string]*graph.Node) {
	classNode = &graph.Node{
		ID: classID, Kind: graph.KindType, Name: name,
		FilePath: filePath, Language: "java",
	}
	b.g.AddNode(classNode)
	b.g.AddEdge(&graph.Edge{From: classID, To: ifaceID, Kind: graph.EdgeImplements, FilePath: filePath, Line: 1})

	methodNodes = map[string]*graph.Node{}
	for _, m := range methods {
		mid := filePath + "::" + name + "." + m
		mn := &graph.Node{
			ID: mid, Kind: graph.KindMethod, Name: m,
			FilePath: filePath, Language: "java",
			Meta: map[string]any{"receiver": name},
		}
		b.g.AddNode(mn)
		methodNodes[m] = mn
	}
	return classNode, methodNodes
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return string(s[0]-32) + s[1:]
}

// --- Go-side tests --------------------------------------------------

func TestResolveTemporalCalls_GoActivityRegistration(t *testing.T) {
	b := newTemporalTestGraph()
	b.addGoFunc("wf/workflow.go::OrderWorkflow", "OrderWorkflow", "wf/workflow.go", "svc")
	call := b.addStubCall("wf/workflow.go::OrderWorkflow", "activity", "ChargeCard", "wf/workflow.go")
	activity := b.addGoFunc("wf/activity.go::ChargeCard", "ChargeCard", "wf/activity.go", "svc")
	b.addGoFunc("wf/main.go::setupWorker", "setupWorker", "wf/main.go", "svc")
	b.addGoRegister("wf/main.go::setupWorker", "activity", "ChargeCard", "wf/main.go")

	resolved := ResolveTemporalCalls(b.g)
	assert.Equal(t, 1, resolved)
	assert.Equal(t, activity.ID, call.To, "stub call must land on the registered activity")
	assert.Equal(t, graph.OriginASTResolved, call.Origin)
	assert.Equal(t, 0.9, call.Confidence)
	assert.Equal(t, "EXTRACTED", call.ConfidenceLabel)
	assert.Equal(t, graph.OriginASTResolved, call.Meta["temporal_resolution"])

	assert.Equal(t, "activity", activity.Meta["temporal_role"], "registered activity must carry temporal_role meta")
	assert.Equal(t, "ChargeCard", activity.Meta["temporal_name"])

	require.Len(t, b.g.GetInEdges(activity.ID), 1, "activity must see the inbound call edge")
}

func TestResolveTemporalCalls_EnvDefaultResolvesSpeculative(t *testing.T) {
	b := newTemporalTestGraph()
	b.addGoFunc("wf/workflow.go::OrderWorkflow", "OrderWorkflow", "wf/workflow.go", "svc")
	call := b.addStubCallEnvDefault("wf/workflow.go::OrderWorkflow", "activity", "ChargeCard", "wf/workflow.go")
	activity := b.addGoFunc("wf/activity.go::ChargeCard", "ChargeCard", "wf/activity.go", "svc")
	b.addGoFunc("wf/main.go::setupWorker", "setupWorker", "wf/main.go", "svc")
	b.addGoRegister("wf/main.go::setupWorker", "activity", "ChargeCard", "wf/main.go")

	resolved := ResolveTemporalCalls(b.g)
	assert.Equal(t, 1, resolved)
	assert.Equal(t, activity.ID, call.To, "env-default stub must still land on the registered activity")
	assert.Equal(t, graph.OriginSpeculative, call.Origin, "env-default resolution must be speculative tier")
	assert.Less(t, call.Confidence, 0.5, "speculative confidence must be below the inferred threshold")
	assert.Equal(t, true, call.Meta[graph.MetaSpeculative], "env-default edge must be hidden-by-default")
}

func TestResolveTemporalCalls_EnvDefaultUnresolvedStaysPlaceholder(t *testing.T) {
	b := newTemporalTestGraph()
	b.addGoFunc("wf/workflow.go::WF", "WF", "wf/workflow.go", "svc")
	call := b.addStubCallEnvDefault("wf/workflow.go::WF", "activity", "MissingActivity", "wf/workflow.go")

	resolved := ResolveTemporalCalls(b.g)
	assert.Equal(t, 0, resolved)
	assert.Equal(t, temporalStubPlaceholder("activity", "MissingActivity"), call.To)
	_, speculative := call.Meta[graph.MetaSpeculative]
	assert.False(t, speculative, "unresolved env-default edge must not carry the speculative flag")
}

func TestResolveTemporalCalls_GoChildWorkflowRegistration(t *testing.T) {
	b := newTemporalTestGraph()
	b.addGoFunc("a/parent.go::ParentWorkflow", "ParentWorkflow", "a/parent.go", "svc")
	call := b.addStubCall("a/parent.go::ParentWorkflow", "workflow", "ChildWorkflow", "a/parent.go")
	child := b.addGoFunc("a/child.go::ChildWorkflow", "ChildWorkflow", "a/child.go", "svc")
	b.addGoFunc("a/main.go::setup", "setup", "a/main.go", "svc")
	b.addGoRegister("a/main.go::setup", "workflow", "ChildWorkflow", "a/main.go")

	ResolveTemporalCalls(b.g)
	assert.Equal(t, child.ID, call.To)
	assert.Equal(t, "workflow", child.Meta["temporal_role"])
}

func TestResolveTemporalCalls_GoNoRegistrationStaysPlaceholder(t *testing.T) {
	b := newTemporalTestGraph()
	b.addGoFunc("wf/workflow.go::WF", "WF", "wf/workflow.go", "svc")
	call := b.addStubCall("wf/workflow.go::WF", "activity", "MissingActivity", "wf/workflow.go")

	resolved := ResolveTemporalCalls(b.g)
	assert.Equal(t, 0, resolved)
	assert.Equal(t, temporalStubPlaceholder("activity", "MissingActivity"), call.To)
	assert.Empty(t, call.Origin)
}

func TestResolveTemporalCalls_GoIdempotent(t *testing.T) {
	b := newTemporalTestGraph()
	b.addGoFunc("wf/workflow.go::WF", "WF", "wf/workflow.go", "svc")
	call := b.addStubCall("wf/workflow.go::WF", "activity", "Charge", "wf/workflow.go")
	activity := b.addGoFunc("wf/activity.go::Charge", "Charge", "wf/activity.go", "svc")
	b.addGoFunc("wf/main.go::setup", "setup", "wf/main.go", "svc")
	b.addGoRegister("wf/main.go::setup", "activity", "Charge", "wf/main.go")

	first := ResolveTemporalCalls(b.g)
	second := ResolveTemporalCalls(b.g)
	third := ResolveTemporalCalls(b.g)
	assert.Equal(t, 1, first)
	assert.Equal(t, 1, second)
	assert.Equal(t, 1, third)
	assert.Equal(t, activity.ID, call.To)
	require.Len(t, b.g.GetInEdges(activity.ID), 1, "no duplicate inbound edges across re-runs")
}

// TestCrossRepoStringDispatch covers G3: a workflow in repo A dispatches
// the bare activity name "Charge" while the only matching activity FUNCTION
// — "ChargeActivity" — lives (unregistered) in repo B. lookupConvention is
// broadened from a pure suffix check to a suffix-AND-core-contains check, so
// the core "Charge" matches "ChargeActivity" across the repo boundary.
func TestCrossRepoStringDispatch(t *testing.T) {
	b := newTemporalTestGraph()
	b.addGoFunc("a/workflow.go::OrderWorkflow", "OrderWorkflow", "a/workflow.go", "repoA")
	call := b.addStubCall("a/workflow.go::OrderWorkflow", "activity", "Charge", "a/workflow.go")
	activity := b.addGoFunc("b/activity.go::ChargeActivity", "ChargeActivity", "b/activity.go", "repoB")

	resolved := ResolveTemporalCalls(b.g)
	assert.Equal(t, 1, resolved)
	assert.Equal(t, activity.ID, call.To, "cross-repo dispatch must resolve to ChargeActivity")
	// Convention (or fuzzy) tier — never a register-confirmed 0.9.
	assert.NotEmpty(t, call.Origin)
	assert.LessOrEqual(t, call.Confidence, 0.6)
}

// TestCrossRepoStringDispatch_NegativePrecision asserts an UNRELATED activity
// "ProcessActivity" in repo B is never linked to the dispatch name "Charge"
// at high confidence. With only "ProcessActivity" present, "Charge" must not
// resolve through convention; and when both exist, "ChargeActivity" wins
// while "ProcessActivity" stays unlinked.
func TestCrossRepoStringDispatch_NegativePrecision(t *testing.T) {
	b := newTemporalTestGraph()
	b.addGoFunc("a/workflow.go::OrderWorkflow", "OrderWorkflow", "a/workflow.go", "repoA")
	call := b.addStubCall("a/workflow.go::OrderWorkflow", "activity", "Charge", "a/workflow.go")
	unrelated := b.addGoFunc("b/activity.go::ProcessActivity", "ProcessActivity", "b/activity.go", "repoB")

	resolved := ResolveTemporalCalls(b.g)
	// An unrelated activity must not become a high-confidence target for an
	// unrelated dispatch name. If it resolves at all it must be speculative.
	assert.Equal(t, 0, resolved, "unrelated ProcessActivity must not satisfy dispatch \"Charge\"")
	assert.NotEqual(t, unrelated.ID, call.To)
	if call.Confidence > 0 {
		assert.LessOrEqual(t, call.Confidence, 0.5)
		assert.Equal(t, true, call.Meta[graph.MetaSpeculative])
	}

	// Now add the correct activity: it must win, the unrelated one stays
	// unlinked.
	correct := b.addGoFunc("b/charge.go::ChargeActivity", "ChargeActivity", "b/charge.go", "repoB")
	ResolveTemporalCalls(b.g)
	assert.Equal(t, correct.ID, call.To, "ChargeActivity must be preferred over ProcessActivity")
	assert.Empty(t, b.g.GetInEdges(unrelated.ID), "ProcessActivity must have no inbound dispatch edge")
}

// TestFuzzyFallback_SpeculativeTier covers the conservative fuzzy fallback:
// it fires only when exact + convention both fail. The dispatch name is the
// already-suffixed "ChargeActivity". Two suffix-core candidates exist —
// "ChargeActivity" and "ChargebackActivity" — both of which contain the
// convention core "Charge", so broadened convention is AMBIGUOUS and returns
// "" (no same-repo, two cross-repo). Fuzzy then matches on the RAW dispatch
// name "ChargeActivity": only one candidate contains it, so it is linked at
// the speculative tier (confidence ≤ 0.5, MetaSpeculative set, via=fuzzy).
func TestFuzzyFallback_SpeculativeTier(t *testing.T) {
	b := newTemporalTestGraph()
	b.addGoFunc("a/workflow.go::OrderWorkflow", "OrderWorkflow", "a/workflow.go", "repoA")
	call := b.addStubCall("a/workflow.go::OrderWorkflow", "activity", "ChargeActivity", "a/workflow.go")
	activity := b.addGoFunc("b/activity.go::ChargeActivity", "ChargeActivity", "b/activity.go", "repoB")
	// Ambiguating sibling: also matches the convention core "Charge", forcing
	// convention to abstain so fuzzy's raw-name single-match carries it.
	b.addGoFunc("c/activity.go::ChargebackActivity", "ChargebackActivity", "c/activity.go", "repoC")

	ResolveTemporalCalls(b.g)
	assert.Equal(t, activity.ID, call.To, "lone fuzzy candidate must be linked")
	assert.LessOrEqual(t, call.Confidence, 0.5, "fuzzy match must stay at/below speculative confidence")
	assert.Equal(t, true, call.Meta[graph.MetaSpeculative], "fuzzy match must be hidden-by-default")
	assert.Equal(t, graph.OriginSpeculative, call.Origin)
	assert.Equal(t, "fuzzy", call.Meta["temporal_resolution_via"])
}

func TestResolveTemporalCalls_GoReorphanOnHandlerLost(t *testing.T) {
	// First settle the stub call onto a resolved handler, then mutate
	// the call's temporal_name so the next pass can't find a handler
	// for it — the edge must re-orphan to the placeholder and drop
	// its resolution metadata. The same code path runs when the real
	// daemon evicts a register file: the stub-call edge survives the
	// reindex, but the resolver no longer finds a target.
	b := newTemporalTestGraph()
	b.addGoFunc("wf/workflow.go::WF", "WF", "wf/workflow.go", "svc")
	call := b.addStubCall("wf/workflow.go::WF", "activity", "Charge", "wf/workflow.go")
	b.addGoFunc("wf/activity.go::Charge", "Charge", "wf/activity.go", "svc")
	b.addGoFunc("wf/main.go::setup", "setup", "wf/main.go", "svc")
	b.addGoRegister("wf/main.go::setup", "activity", "Charge", "wf/main.go")

	ResolveTemporalCalls(b.g)
	require.NotEqual(t, temporalStubPlaceholder("activity", "Charge"), call.To)
	require.Equal(t, graph.OriginASTResolved, call.Origin)

	// Re-target the stub call at an activity name nothing registers.
	call.Meta["temporal_name"] = "NoSuchActivity"

	resolved := ResolveTemporalCalls(b.g)
	assert.Equal(t, 0, resolved)
	assert.Equal(t, temporalStubPlaceholder("activity", "NoSuchActivity"), call.To)
	assert.Empty(t, call.Origin)
	_, hasRes := call.Meta["temporal_resolution"]
	assert.False(t, hasRes, "temporal_resolution meta must be cleared on re-orphan")
}

func TestResolveTemporalCalls_GoSameRepoPreference(t *testing.T) {
	b := newTemporalTestGraph()
	b.addGoFunc("svc/workflow.go::WF", "WF", "svc/workflow.go", "svc")
	call := b.addStubCall("svc/workflow.go::WF", "activity", "Charge", "svc/workflow.go")
	local := b.addGoFunc("svc/activity.go::Charge", "Charge", "svc/activity.go", "svc")
	b.addGoFunc("other/activity.go::Charge", "Charge", "other/activity.go", "other")
	b.addGoFunc("svc/main.go::setup", "setup", "svc/main.go", "svc")
	b.addGoRegister("svc/main.go::setup", "activity", "Charge", "svc/main.go")

	ResolveTemporalCalls(b.g)
	assert.Equal(t, local.ID, call.To, "same-repo activity must win the tie-break")
}

func TestResolveTemporalCalls_GoLocalActivityFlagPreserved(t *testing.T) {
	b := newTemporalTestGraph()
	b.addGoFunc("wf/workflow.go::WF", "WF", "wf/workflow.go", "svc")
	call := b.addStubCall("wf/workflow.go::WF", "activity", "Lookup", "wf/workflow.go")
	call.Meta["temporal_local"] = true
	b.addGoFunc("wf/activity.go::Lookup", "Lookup", "wf/activity.go", "svc")
	b.addGoFunc("wf/main.go::setup", "setup", "wf/main.go", "svc")
	b.addGoRegister("wf/main.go::setup", "activity", "Lookup", "wf/main.go")

	ResolveTemporalCalls(b.g)
	assert.Equal(t, true, call.Meta["temporal_local"], "local-activity flag must survive the rewrite")
}

func TestResolveTemporalCalls_GoCrossRepoFlowsThroughDetector(t *testing.T) {
	// Workflow in repo "wf", activity in repo "acts", worker setup in
	// repo "wf". After resolution the cross-repo edge layer must
	// materialise a cross_repo_calls parallel edge.
	b := newTemporalTestGraph()
	b.addGoFunc("wf/workflow.go::WF", "WF", "wf/workflow.go", "wf")
	call := b.addStubCall("wf/workflow.go::WF", "activity", "Charge", "wf/workflow.go")
	activity := b.addGoFunc("acts/activity.go::Charge", "Charge", "acts/activity.go", "acts")
	b.addGoFunc("wf/main.go::setup", "setup", "wf/main.go", "wf")
	b.addGoRegister("wf/main.go::setup", "activity", "Charge", "wf/main.go")

	ResolveTemporalCalls(b.g)
	require.Equal(t, activity.ID, call.To)

	emitted := DetectCrossRepoEdges(b.g)
	assert.GreaterOrEqual(t, emitted, 1, "resolved cross-repo Temporal call must materialise a cross_repo_calls edge")
	cr := firstOutEdgeByKind(b.g, "wf/workflow.go::WF", graph.EdgeCrossRepoCalls)
	require.NotNil(t, cr)
	assert.Equal(t, activity.ID, cr.To)
}

// --- Java-side tests ------------------------------------------------

func TestResolveTemporalCalls_JavaActivityInterfacePropagation(t *testing.T) {
	b := newTemporalTestGraph()
	iface, ifaceMethods := b.addJavaInterface(
		"OrderActivities.java::OrderActivities", "OrderActivities", "OrderActivities.java",
		javaActivityIfaceAnnoID, "chargeCard", "shipOrder",
	)
	_, implMethods := b.addJavaImpl(
		"OrderActivitiesImpl.java::OrderActivitiesImpl", "OrderActivitiesImpl",
		"OrderActivitiesImpl.java", iface.ID, "chargeCard", "shipOrder",
	)

	ResolveTemporalCalls(b.g)

	assert.Equal(t, "activity_interface", iface.Meta["temporal_role"])
	assert.Equal(t, "activity", ifaceMethods["chargeCard"].Meta["temporal_role"], "interface methods tagged")
	assert.Equal(t, "activity", ifaceMethods["shipOrder"].Meta["temporal_role"])
	assert.Equal(t, "activity", implMethods["chargeCard"].Meta["temporal_role"], "impl methods tagged via interface chain")
	assert.Equal(t, "activity", implMethods["shipOrder"].Meta["temporal_role"])
}

func TestResolveTemporalCalls_JavaWorkflowInterfacePropagation(t *testing.T) {
	b := newTemporalTestGraph()
	iface, ifaceMethods := b.addJavaInterface(
		"OrderWorkflow.java::OrderWorkflow", "OrderWorkflow", "OrderWorkflow.java",
		javaWorkflowIfaceAnnoID, "processOrder",
	)
	_, implMethods := b.addJavaImpl(
		"OrderWorkflowImpl.java::OrderWorkflowImpl", "OrderWorkflowImpl",
		"OrderWorkflowImpl.java", iface.ID, "processOrder",
	)

	ResolveTemporalCalls(b.g)

	assert.Equal(t, "workflow_interface", iface.Meta["temporal_role"])
	assert.Equal(t, "workflow", ifaceMethods["processOrder"].Meta["temporal_role"])
	assert.Equal(t, "workflow", implMethods["processOrder"].Meta["temporal_role"])
}

func TestResolveTemporalCalls_JavaSignalAndQueryMethods(t *testing.T) {
	b := newTemporalTestGraph()
	// Method-level annotations on a workflow class — no interface-level
	// annotation; signal / query roles still get stamped.
	mid := "Workflow.java::handleSignal"
	method := &graph.Node{
		ID: mid, Kind: graph.KindMethod, Name: "handleSignal",
		FilePath: "Workflow.java", Language: "java",
		StartLine: 20,
	}
	b.g.AddNode(method)
	b.g.AddNode(&graph.Node{
		ID: javaSignalMethodID, Kind: graph.KindType, Name: "SignalMethod",
		FilePath: "Workflow.java", Language: "java",
		Meta: map[string]any{"kind": "annotation", "synthetic": true},
	})
	b.g.AddEdge(&graph.Edge{From: mid, To: javaSignalMethodID, Kind: graph.EdgeAnnotated, FilePath: "Workflow.java", Line: 19})

	qid := "Workflow.java::currentStatus"
	qmethod := &graph.Node{
		ID: qid, Kind: graph.KindMethod, Name: "currentStatus",
		FilePath: "Workflow.java", Language: "java",
		StartLine: 25,
	}
	b.g.AddNode(qmethod)
	b.g.AddNode(&graph.Node{
		ID: javaQueryMethodID, Kind: graph.KindType, Name: "QueryMethod",
		FilePath: "Workflow.java", Language: "java",
		Meta: map[string]any{"kind": "annotation", "synthetic": true},
	})
	b.g.AddEdge(&graph.Edge{From: qid, To: javaQueryMethodID, Kind: graph.EdgeAnnotated, FilePath: "Workflow.java", Line: 24})

	ResolveTemporalCalls(b.g)

	assert.Equal(t, "signal", method.Meta["temporal_role"])
	assert.Equal(t, "query", qmethod.Meta["temporal_role"])
}

func TestResolveTemporalCalls_JavaInterfaceMethodsScopedByLineRange(t *testing.T) {
	// Two interfaces in the same file: only the @ActivityInterface
	// methods get tagged, not the methods on the unrelated interface
	// that follows it.
	b := newTemporalTestGraph()
	b.addJavaInterface(
		"both.java::ActivityIface", "ActivityIface", "both.java",
		javaActivityIfaceAnnoID, "doWork",
	)
	// Inject a second, unrelated interface in the same file with
	// methods OUTSIDE the first interface's line range.
	other := &graph.Node{
		ID: "both.java::OtherIface", Kind: graph.KindInterface, Name: "OtherIface",
		FilePath: "both.java", Language: "java",
		StartLine: 40, EndLine: 60,
	}
	b.g.AddNode(other)
	otherMethod := &graph.Node{
		ID: "both.java::unrelated", Kind: graph.KindMethod, Name: "unrelated",
		FilePath: "both.java", Language: "java",
		StartLine: 45,
	}
	b.g.AddNode(otherMethod)

	ResolveTemporalCalls(b.g)

	_, hasRole := otherMethod.Meta["temporal_role"]
	assert.False(t, hasRole, "unrelated interface's methods must not get tagged")
}

func TestResolveTemporalCalls_RoleStampingIsIdempotent(t *testing.T) {
	b := newTemporalTestGraph()
	_, methods := b.addJavaInterface(
		"Acts.java::Acts", "Acts", "Acts.java",
		javaActivityIfaceAnnoID, "doIt",
	)
	for range 5 {
		ResolveTemporalCalls(b.g)
	}
	assert.Equal(t, "activity", methods["doIt"].Meta["temporal_role"])
}

// --- Iterative wrapper-following tests (G5) ---------------------------------

// addWrapperStub adds a temporal.stub edge with temporal_name_param set,
// marking the function as a name-forwarding wrapper (its body calls
// ExecuteActivity with `param` as the name argument at `pos`).
//
// PURPOSE: helper that builds the parser-side graph shape the wrapper resolver
// consumes.
// RATIONALE: mirrors what the Go extractor emits for `ExecuteActivity(ctx,
// name, ...)` patterns.
// KEYWORDS: test-helper, wrapper-stub, temporal_name_param
func (b *temporalTestGraph) addWrapperStub(funcID, kind, param string, pos int, filePath string) *graph.Edge {
	// param node — the resolver looks up `funcID#param:<param>` to find pos
	b.g.AddNode(&graph.Node{
		ID: funcID + "#param:" + param, Kind: graph.KindParam, Name: param,
		FilePath: filePath, Language: "go",
		Meta: map[string]any{"position": pos},
	})
	e := &graph.Edge{
		From: funcID, To: temporalStubPlaceholder(kind, "__"+param+"__"),
		Kind: graph.EdgeCalls, FilePath: filePath, Line: 5,
		Meta: map[string]any{
			"via":                  "temporal.stub",
			"temporal_kind":        kind,
			"temporal_name":        "__" + param + "__",
			"temporal_name_param":  param,
		},
	}
	b.g.AddEdge(e)
	return e
}

// addWrapperCall adds an EdgeCalls from callerID to calleeID carrying
// arg_names so the wrapper resolver can read the argument at the callee's
// name-parameter position.
//
// PURPOSE: helper that builds the call-site graph shape the wrapper resolver
// consumes.
// RATIONALE: mirrors what the Go extractor emits for `wrapper(ctx, "ActName")`
// call sites.
// KEYWORDS: test-helper, wrapper-call, arg_names
func (b *temporalTestGraph) addWrapperCall(callerID, calleeID, calleeName, filePath string, line int, args ...string) *graph.Edge {
	e := &graph.Edge{
		From: callerID, To: calleeID,
		Kind: graph.EdgeCalls, FilePath: filePath, Line: line,
		Meta: map[string]any{
			"arg_names": args,
			"callee":    calleeName,
		},
	}
	b.g.AddEdge(e)
	return e
}

// wfChargeStub returns the temporal.stub edge from wfID that carries
// temporal_name == "ChargeActivity", or nil if none exists.
//
// PURPOSE: assertion helper used by iterative-wrapper tests.
// KEYWORDS: test-helper, assert
func wfChargeStub(g graph.Store, wfID string) *graph.Edge {
	for _, e := range g.GetOutEdges(wfID) {
		if e == nil || e.Meta == nil {
			continue
		}
		if v, _ := e.Meta["via"].(string); v != "temporal.stub" {
			continue
		}
		if n, _ := e.Meta["temporal_name"].(string); n == "ChargeActivity" {
			return e
		}
	}
	return nil
}

// buildDepth2WrapperGraph constructs:
//
//	WF → Outer("ChargeActivity")
//	Outer → Inner(outerName)          // forwards its own param
//	Inner → ExecuteActivity(innerName) // parser-discovered wrapper
//
// Only Inner is a parser-discovered wrapper at the first pass. Outer
// becomes a wrapper only after the pass propagates temporal_name_param to
// it (because Outer forwards its own parameter to Inner). The workflow's
// "ChargeActivity" stub therefore lands only on the SECOND iteration —
// which is exactly what the iterative loop exists to deliver.
func buildDepth2WrapperGraph() (*temporalTestGraph, string) {
	b := newTemporalTestGraph()
	const (
		wfID    = "repo/wf.go::WF"
		outerID = "repo/w.go::Outer"
		innerID = "repo/w.go::Inner"
	)
	b.addGoFunc(wfID, "WF", "repo/wf.go", "repo")
	b.addGoFunc(outerID, "Outer", "repo/w.go", "repo")
	b.addGoFunc(innerID, "Inner", "repo/w.go", "repo")

	// Inner is the real dispatcher: ExecuteActivity(ctx, innerName, …) at pos 1.
	b.addWrapperStub(innerID, "activity", "innerName", 1, "repo/w.go")
	// Outer's forwarded parameter sits at position 1 too.
	b.g.AddNode(&graph.Node{
		ID: outerID + "#param:outerName", Kind: graph.KindParam, Name: "outerName",
		FilePath: "repo/w.go", Language: "go", Meta: map[string]any{"position": 1},
	})

	// Outer forwards its OWN param (outerName) to Inner at Inner's name-pos.
	b.addWrapperCall(outerID, innerID, "Inner", "repo/w.go", 20, "ctx", "outerName")
	// WF passes the LITERAL "ChargeActivity" to Outer at Outer's name-pos.
	b.addWrapperCall(wfID, outerID, "Outer", "repo/wf.go", 30, "ctx", "ChargeActivity")

	return b, wfID
}

// TestIterativeWrapperFollowing is the depth-2 headline: a wrapper (Outer)
// that forwards its own parameter into another wrapper (Inner) must itself
// become a wrapper, so the workflow that calls Outer with a literal name
// resolves through two hops. A single wrapper-following pass cannot do
// this (Outer is not yet a wrapper when its call sites are scanned); the
// iterative loop closes the gap on the second iteration.
func TestIterativeWrapperFollowing(t *testing.T) {
	b, wfID := buildDepth2WrapperGraph()

	// A single pass must NOT yet mint WF's "ChargeActivity" stub — proves
	// the depth-1 limitation the loop overcomes.
	resolveTemporalWrapperCalls(b.g)
	assert.Nil(t, wfChargeStub(b.g, wfID),
		"depth-1: a single wrapper-following pass cannot reach through Outer→Inner")

	// The full gate-controlled entry point runs the loop; the depth-2
	// dispatch must now land on the registered ChargeActivity.
	b2, wfID2 := buildDepth2WrapperGraph()
	// Register the activity so the main stub resolver can land the edge.
	b2.addGoFunc("repo/act.go::ChargeActivity", "ChargeActivity", "repo/act.go", "repo")
	b2.addGoFunc("repo/main.go::setup", "setup", "repo/main.go", "repo")
	b2.addGoRegister("repo/main.go::setup", "activity", "ChargeActivity", "repo/main.go")

	ResolveTemporalCalls(b2.g)

	stub := wfChargeStub(b2.g, wfID2)
	require.NotNil(t, stub, "depth-2: iterative loop must mint WF's ChargeActivity stub")
	assert.Equal(t, "repo/act.go::ChargeActivity", stub.To,
		"depth-2 dispatch must land on the registered ChargeActivity")
}

// TestIterativeWrapperFollowing_ConvergesWithNoGrowth pins the
// convergence contract: once the depth-2 fixed point is reached, further
// passes add zero new temporal.stub edges. graph.AddEdge dedupes, so the
// pass is idempotent and the loop terminates rather than running away.
func TestIterativeWrapperFollowing_ConvergesWithNoGrowth(t *testing.T) {
	b, _ := buildDepth2WrapperGraph()

	resolveTemporalWrapperCalls(b.g) // iteration 1: Outer becomes a wrapper
	resolveTemporalWrapperCalls(b.g) // iteration 2: WF stub minted
	stable := countTemporalStubEdges(b.g)

	// Third (and any further) pass must be a fixed point — no growth.
	resolveTemporalWrapperCalls(b.g)
	assert.Equal(t, stable, countTemporalStubEdges(b.g),
		"wrapper-following must converge: no new stubs after the fixed point")

	resolveTemporalWrapperCalls(b.g)
	assert.Equal(t, stable, countTemporalStubEdges(b.g),
		"repeated passes stay at the fixed point (idempotent)")
}

// TestSignalCrossRepo proves that SignalExternalWorkflow(…, "save-order-signal")
// in repoA links to SetSignalHandler(…, "save-order-signal") in repoB via
// resolveTemporalSignalQueryLinks. The handler-marker edge (via=temporal.handler)
// and the send-site edge (via=temporal.signal-send) are in different repos,
// verifying that the function is truly cross-repo.
//
// PURPOSE: Gap-4 coverage — signal/query cross-repo verification.
// RATIONALE: resolveTemporalSignalQueryLinks iterates all edges without a
// repo filter, so cross-repo linking should work without extra code.
// KEYWORDS: signal, cross-repo, tdd, gap4
func TestSignalCrossRepo(t *testing.T) {
	b := newTemporalTestGraph()

	const (
		senderID   = "repoA/sender.go::SenderWF"
		receiverID = "repoB/receiver.go::ReceiverWF"
		signalName = "save-order-signal"
		signalKind = "signal"
	)

	// repoA: SenderWF calls SignalExternalWorkflow with "save-order-signal".
	b.addGoFunc(senderID, "SenderWF", "repoA/sender.go", "repoA")
	b.g.AddEdge(&graph.Edge{
		From:     senderID,
		To:       "unresolved::extern::go.temporal.io/sdk/client::SignalExternalWorkflow",
		Kind:     graph.EdgeCalls,
		FilePath: "repoA/sender.go",
		Line:     20,
		Meta: map[string]any{
			"via":           "temporal.signal-send",
			"temporal_kind": signalKind,
			"temporal_name": signalName,
		},
	})

	// repoB: ReceiverWF declares SetSignalHandler for "save-order-signal".
	b.addGoFunc(receiverID, "ReceiverWF", "repoB/receiver.go", "repoB")
	b.g.AddEdge(&graph.Edge{
		From:     receiverID,
		To:       "unresolved::extern::go.temporal.io/sdk/workflow::GetSignalChannel",
		Kind:     graph.EdgeCalls,
		FilePath: "repoB/receiver.go",
		Line:     15,
		Meta: map[string]any{
			"via":           "temporal.handler",
			"temporal_kind": signalKind,
			"temporal_name": signalName,
		},
	})

	ResolveTemporalCalls(b.g)

	// Expect an EdgeCalls from SenderWF → ReceiverWF with via=temporal.signal-link.
	var linkEdge *graph.Edge
	for e := range b.g.EdgesByKind(graph.EdgeCalls) {
		if e == nil || e.Meta == nil {
			continue
		}
		if via, _ := e.Meta["via"].(string); via != "temporal.signal-link" {
			continue
		}
		if e.From == senderID && e.To == receiverID {
			linkEdge = e
			break
		}
	}
	require.NotNil(t, linkEdge,
		"expected a temporal.signal-link edge from SenderWF (repoA) to ReceiverWF (repoB)")
	assert.Equal(t, signalName, linkEdge.Meta["temporal_name"],
		"signal-link must carry the signal name")
	assert.Equal(t, signalKind, linkEdge.Meta["temporal_kind"],
		"signal-link must carry the signal kind")
}

// TestIterativeWrapperFollowing_Depth1Regression guards the depth-1 path:
// a single wrapper called directly by a workflow with a string literal
// still resolves in exactly one effective hop, unchanged by the loop.
func TestIterativeWrapperFollowing_Depth1Regression(t *testing.T) {
	b := newTemporalTestGraph()
	const (
		wfID      = "repo/wf.go::WF"
		wrapperID = "repo/w.go::runActivity"
	)
	b.addGoFunc(wfID, "WF", "repo/wf.go", "repo")
	b.addGoFunc(wrapperID, "runActivity", "repo/w.go", "repo")
	// runActivity dispatches ExecuteActivity(ctx, name, …) — name at pos 1.
	b.addWrapperStub(wrapperID, "activity", "name", 1, "repo/w.go")
	// WF calls the wrapper with the literal "ChargeActivity".
	b.addWrapperCall(wfID, wrapperID, "runActivity", "repo/wf.go", 10, "ctx", "ChargeActivity")

	// Register ChargeActivity so the main resolver can land the edge.
	b.addGoFunc("repo/act.go::ChargeActivity", "ChargeActivity", "repo/act.go", "repo")
	b.addGoFunc("repo/main.go::setup", "setup", "repo/main.go", "repo")
	b.addGoRegister("repo/main.go::setup", "activity", "ChargeActivity", "repo/main.go")

	ResolveTemporalCalls(b.g)

	stub := wfChargeStub(b.g, wfID)
	require.NotNil(t, stub, "depth-1: wrapper call with literal must still resolve")
	assert.Equal(t, "repo/act.go::ChargeActivity", stub.To,
		"depth-1 dispatch must land on ChargeActivity")
}

// --- Func-returning-literal dispatch (G2) ---------------------------

func TestFuncConstResolution(t *testing.T) {
	b := newTemporalTestGraph()
	b.addGoFunc("wf/workflow.go::OrderWorkflow", "OrderWorkflow", "wf/workflow.go", "svc")
	// Dispatch via a const-returning func: ExecuteActivity(ctx, GetChargeName(), …)
	call := b.addStubCallNameFunc("wf/workflow.go::OrderWorkflow", "activity", "GetChargeName", "wf/workflow.go")
	// GetChargeName() returns "ChargeCard".
	b.addGoConstReturnFunc("wf/names.go::GetChargeName", "GetChargeName", "wf/names.go", "svc", "ChargeCard")
	// The activity registered under "ChargeCard".
	activity := b.addGoFunc("wf/activity.go::ChargeCard", "ChargeCard", "wf/activity.go", "svc")
	b.addGoFunc("wf/main.go::setupWorker", "setupWorker", "wf/main.go", "svc")
	b.addGoRegister("wf/main.go::setupWorker", "activity", "ChargeCard", "wf/main.go")

	resolved := ResolveTemporalCalls(b.g)
	assert.Equal(t, 1, resolved)
	assert.Equal(t, activity.ID, call.To,
		"func-call dispatch must land on the activity via the const-return literal")
	assert.Equal(t, graph.OriginASTResolved, call.Origin,
		"func-return is deterministic → ast_resolved tier")
	assert.Equal(t, 0.9, call.Confidence)
	assert.Equal(t, "ChargeCard", call.Meta["temporal_const_value"],
		"resolved edge must record the literal the func returns")
	_, speculative := call.Meta[graph.MetaSpeculative]
	assert.False(t, speculative, "func-return resolution must not be speculative")
}

func TestFuncConstResolution_UnknownFuncStaysPlaceholder(t *testing.T) {
	b := newTemporalTestGraph()
	b.addGoFunc("wf/workflow.go::WF", "WF", "wf/workflow.go", "svc")
	call := b.addStubCallNameFunc("wf/workflow.go::WF", "activity", "MissingFunc", "wf/workflow.go")

	resolved := ResolveTemporalCalls(b.g)
	assert.Equal(t, 0, resolved)
	assert.Equal(t, temporalStubPlaceholder("activity", "MissingFunc"), call.To)
	assert.Empty(t, call.Origin)
}
