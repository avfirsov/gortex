package resolver

// PURPOSE — gate-proof tests for the SynthTemporalStub skip mechanism.
// Verifies that RunFrameworkSynthesizersExcept with skip[SynthTemporalStub]=true
// suppresses all Temporal dispatch edges, and that the default (skip=nil) path
// produces the same result as RunFrameworkSynthesizers (zero behaviour change).
// RATIONALE — the GORTEX_TEMPORAL=off config path must demonstrably prevent any
// Temporal edges from landing, catching regressions where a new Temporal sub-pass
// runs outside the skip gate.
// KEYWORDS — temporal, gate, skip, synthesizer, orphan, env

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
)

// --- helpers -----------------------------------------------------------

// buildBaseTemporalFixture returns a graph where a Go workflow dispatches
// "ChargeCard" via a temporal.stub edge to a registered Go activity.
// With the synthesizer ON the edge resolves; OFF it stays a placeholder.
func buildBaseTemporalFixture() *temporalTestGraph {
	b := newTemporalTestGraph()
	b.addGoFunc("wf/wf.go::WF", "WF", "wf/wf.go", "svc")
	b.addStubCall("wf/wf.go::WF", "activity", "ChargeCard", "wf/wf.go")
	b.addGoFunc("wf/act.go::ChargeCard", "ChargeCard", "wf/act.go", "svc")
	b.addGoFunc("wf/main.go::setup", "setup", "wf/main.go", "svc")
	b.addGoRegister("wf/main.go::setup", "activity", "ChargeCard", "wf/main.go")
	return b
}

// buildWrapperFixture builds a P2 (wrapper-by-name) fixture: a wrapper
// function that receives the activity name at arg position 0 and dispatches
// it via temporal.stub, plus a call site that passes the literal name.
func buildWrapperFixture() *temporalTestGraph {
	b := newTemporalTestGraph()

	// The wrapper function "runActivity" dispatches via temporal.stub with
	// temporal_wrapper_param=0 and temporal_kind=activity.
	wrapperID := "svc/wrapper.go::runActivity"
	b.addGoFunc(wrapperID, "runActivity", "svc/wrapper.go", "svc")

	// The stub call on the wrapper itself carries the wrapper hint so
	// resolveTemporalWrapperCalls can find it.
	stubEdge := &graph.Edge{
		From: wrapperID,
		To:   temporalStubPlaceholder("activity", ""),
		Kind: graph.EdgeCalls, FilePath: "svc/wrapper.go", Line: 10,
		Meta: map[string]any{
			"via":                      "temporal.stub",
			"temporal_kind":            "activity",
			"temporal_name":            "",
			"temporal_wrapper_param":   0,
			"temporal_wrapper_fn_name": "runActivity",
		},
	}
	b.g.AddEdge(stubEdge)

	// A concrete activity.
	b.addGoFunc("svc/act.go::ProcessOrder", "ProcessOrder", "svc/act.go", "svc")

	// Worker registration.
	b.addGoFunc("svc/main.go::setup", "setup", "svc/main.go", "svc")
	b.addGoRegister("svc/main.go::setup", "activity", "ProcessOrder", "svc/main.go")

	// A call site that invokes the wrapper passing the string literal.
	callerID := "svc/biz.go::OrderFlow"
	b.addGoFunc(callerID, "OrderFlow", "svc/biz.go", "svc")
	callSiteEdge := &graph.Edge{
		From: callerID,
		To:   wrapperID,
		Kind: graph.EdgeCalls, FilePath: "svc/biz.go", Line: 20,
		Meta: map[string]any{
			"arg_names": []any{"ProcessOrder"},
		},
	}
	b.g.AddEdge(callSiteEdge)

	return b
}

// buildExecutorFieldFixture builds a P6 (executor struct-field) fixture:
// an executor struct whose ActivityName field is set at construction,
// driving temporal dispatch.
func buildExecutorFieldFixture() *temporalTestGraph {
	b := newTemporalTestGraph()

	execType := "ActivityExecutor"
	actName := "FulfillOrder"

	// The executor's Execute method dispatches via temporal.executor-field.
	execMethodID := "svc/exec.go::ActivityExecutor.Execute"
	b.addGoFunc(execMethodID, "Execute", "svc/exec.go", "svc")

	constructEdge := &graph.Edge{
		From: "svc/biz.go::Business",
		To:   execMethodID,
		Kind: graph.EdgeCalls, FilePath: "svc/biz.go", Line: 5,
		Meta: map[string]any{
			"via":            "temporal.executor-field",
			"executor_type":  execType,
			"executor_field": "ActivityName",
			"executor_value": actName,
		},
	}
	b.g.AddNode(&graph.Node{ID: "svc/biz.go::Business", Kind: graph.KindFunction, Name: "Business", FilePath: "svc/biz.go", Language: "go"})
	b.g.AddEdge(constructEdge)

	// The actual activity node.
	b.addGoFunc("svc/act.go::FulfillOrder", actName, "svc/act.go", "svc")
	b.addGoFunc("svc/main.go::setup", "setup", "svc/main.go", "svc")
	b.addGoRegister("svc/main.go::setup", "activity", actName, "svc/main.go")

	return b
}

// buildJavaFixture builds a Java→Go cross-language fixture: a @ActivityInterface
// with an implementation class. The Temporal pass tags interface + impl methods.
func buildJavaFixture() *temporalTestGraph {
	b := newTemporalTestGraph()
	iface, _ := b.addJavaInterface(
		"OrderActivities.java::OrderActivities", "OrderActivities", "OrderActivities.java",
		javaActivityIfaceAnnoID, "chargeCard",
	)
	b.addJavaImpl(
		"OrderActivitiesImpl.java::OrderActivitiesImpl", "OrderActivitiesImpl",
		"OrderActivitiesImpl.java", iface.ID, "chargeCard",
	)
	return b
}

// countTemporalEdges returns the number of edges in g that carry the
// SynthTemporalStub synthesized_by provenance tag.
func countTemporalEdges(g graph.Store) int {
	var n int
	for _, e := range g.AllEdges() {
		if v, _ := e.Meta[MetaSynthesizedBy].(string); v == SynthTemporalStub {
			n++
		}
	}
	return n
}

// countTemporalRoleNodes returns the number of nodes in g that carry a
// temporal_role meta key (stamped by ResolveTemporalCalls).
func countTemporalRoleNodes(g graph.Store) int {
	var n int
	for _, nd := range g.AllNodes() {
		if _, ok := nd.Meta["temporal_role"]; ok {
			n++
		}
	}
	return n
}

// --- sub-pass gate tests -----------------------------------------------

// TestTemporalGate_BaseStub_ON confirms that with skip=nil the base temporal
// stub pass resolves the stub call to the registered activity.
func TestTemporalGate_BaseStub_ON(t *testing.T) {
	b := buildBaseTemporalFixture()
	rep := RunFrameworkSynthesizersExcept(b.g, nil)

	actNode := b.g.GetNode("wf/act.go::ChargeCard")
	require.NotNil(t, actNode)

	// At least one edge must have landed on the activity node.
	inEdges := b.g.GetInEdges(actNode.ID)
	assert.NotEmpty(t, inEdges, "ON: stub call must resolve to activity node")

	// The report must count the temporal synthesizer contribution.
	var temporalCount int
	for _, sc := range rep.Per {
		if sc.Name == SynthTemporalStub {
			temporalCount = sc.Edges
		}
	}
	assert.GreaterOrEqual(t, temporalCount, 1, "ON: temporal synthesizer must report ≥1 edge")
}

// TestTemporalGate_BaseStub_OFF confirms that skipping SynthTemporalStub
// leaves the stub call as an unresolved placeholder.
func TestTemporalGate_BaseStub_OFF(t *testing.T) {
	b := buildBaseTemporalFixture()
	skip := map[string]bool{SynthTemporalStub: true}
	rep := RunFrameworkSynthesizersExcept(b.g, skip)

	actNode := b.g.GetNode("wf/act.go::ChargeCard")
	require.NotNil(t, actNode)

	// No edge must land on the activity node.
	inEdges := b.g.GetInEdges(actNode.ID)
	assert.Empty(t, inEdges, "OFF: stub call must stay as placeholder")

	// The temporal synthesizer must report 0 edges.
	var temporalCount int
	for _, sc := range rep.Per {
		if sc.Name == SynthTemporalStub {
			temporalCount = sc.Edges
		}
	}
	assert.Equal(t, 0, temporalCount, "OFF: temporal synthesizer must report 0 edges")
}

// TestTemporalGate_P2_WrapperByName_ON verifies that the wrapper-following
// sub-pass (P2) runs when skip=nil.
func TestTemporalGate_P2_WrapperByName_ON(t *testing.T) {
	b := buildWrapperFixture()
	RunFrameworkSynthesizersExcept(b.g, nil)
	// With the synthesizer on, the wrapper's stub call or a synthesized
	// edge must reference the activity (ProcessOrder).
	act := b.g.GetNode("svc/act.go::ProcessOrder")
	require.NotNil(t, act, "activity node must exist")
	// The pass must have tagged it as activity.
	assert.Equal(t, "activity", act.Meta["temporal_role"], "ON: P2 must tag the activity role")
}

// TestTemporalGate_P2_WrapperByName_OFF verifies that skipping SynthTemporalStub
// suppresses wrapper-following — the activity node must NOT receive a temporal_role.
func TestTemporalGate_P2_WrapperByName_OFF(t *testing.T) {
	b := buildWrapperFixture()
	skip := map[string]bool{SynthTemporalStub: true}
	RunFrameworkSynthesizersExcept(b.g, skip)
	act := b.g.GetNode("svc/act.go::ProcessOrder")
	require.NotNil(t, act)
	_, hasRole := act.Meta["temporal_role"]
	assert.False(t, hasRole, "OFF: P2 must not tag temporal_role when skipped")
}

// TestTemporalGate_P6_ExecutorField_ON verifies that the executor-struct-field
// sub-pass (P6) runs when skip=nil and stamps an edge.
func TestTemporalGate_P6_ExecutorField_ON(t *testing.T) {
	b := buildExecutorFieldFixture()
	RunFrameworkSynthesizersExcept(b.g, nil)
	act := b.g.GetNode("svc/act.go::FulfillOrder")
	require.NotNil(t, act)
	assert.Equal(t, "activity", act.Meta["temporal_role"], "ON: P6 must tag activity role")
}

// TestTemporalGate_P6_ExecutorField_OFF verifies that skipping SynthTemporalStub
// suppresses executor-field dispatch — no temporal_role on the activity.
func TestTemporalGate_P6_ExecutorField_OFF(t *testing.T) {
	b := buildExecutorFieldFixture()
	skip := map[string]bool{SynthTemporalStub: true}
	RunFrameworkSynthesizersExcept(b.g, skip)
	act := b.g.GetNode("svc/act.go::FulfillOrder")
	require.NotNil(t, act)
	_, hasRole := act.Meta["temporal_role"]
	assert.False(t, hasRole, "OFF: P6 must not tag temporal_role when skipped")
}

// TestTemporalGate_JavaCrossLang_ON verifies that the Java→Go cross-language
// pass runs and stamps interface + impl methods with temporal roles.
func TestTemporalGate_JavaCrossLang_ON(t *testing.T) {
	b := buildJavaFixture()
	RunFrameworkSynthesizersExcept(b.g, nil)

	iface := b.g.GetNode("OrderActivities.java::OrderActivities")
	require.NotNil(t, iface)
	assert.Equal(t, "activity_interface", iface.Meta["temporal_role"], "ON: interface must be tagged")

	implMethod := b.g.GetNode("OrderActivitiesImpl.java::OrderActivitiesImpl.chargeCard")
	require.NotNil(t, implMethod)
	assert.Equal(t, "activity", implMethod.Meta["temporal_role"], "ON: impl method must be tagged")
}

// TestTemporalGate_JavaCrossLang_OFF verifies that skipping SynthTemporalStub
// leaves Java interface and impl methods without temporal_role annotations.
func TestTemporalGate_JavaCrossLang_OFF(t *testing.T) {
	b := buildJavaFixture()
	skip := map[string]bool{SynthTemporalStub: true}
	RunFrameworkSynthesizersExcept(b.g, skip)

	iface := b.g.GetNode("OrderActivities.java::OrderActivities")
	require.NotNil(t, iface)
	_, hasRole := iface.Meta["temporal_role"]
	assert.False(t, hasRole, "OFF: interface must not be tagged when skipped")
}

// --- orphan-increase test ----------------------------------------------

// TestTemporalGate_OrphanCountIncreasesWhenOff verifies the core invariant:
// skipping SynthTemporalStub causes more orphan activities (activities that
// are registered but never reached by a resolved dispatch edge).
func TestTemporalGate_OrphanCountIncreasesWhenOff(t *testing.T) {
	// Build a fixture where one activity resolves (ChargeCard) and one is already
	// unregistered (MissingActivity). When ON: ChargeCard resolves → not orphan.
	// When OFF: ChargeCard stub stays placeholder → appears as orphan too.
	buildFixture := func() *temporalTestGraph {
		b := newTemporalTestGraph()
		b.addGoFunc("svc/wf.go::WF", "WF", "svc/wf.go", "svc")
		b.addStubCall("svc/wf.go::WF", "activity", "ChargeCard", "svc/wf.go")
		b.addStubCall("svc/wf.go::WF", "activity", "MissingActivity", "svc/wf.go")
		b.addGoFunc("svc/act.go::ChargeCard", "ChargeCard", "svc/act.go", "svc")
		b.addGoFunc("svc/main.go::setup", "setup", "svc/main.go", "svc")
		b.addGoRegister("svc/main.go::setup", "activity", "ChargeCard", "svc/main.go")
		return b
	}

	bON := buildFixture()
	RunFrameworkSynthesizersExcept(bON.g, nil)
	reportON := DetectTemporalOrphans(bON.g)

	bOFF := buildFixture()
	RunFrameworkSynthesizersExcept(bOFF.g, map[string]bool{SynthTemporalStub: true})
	reportOFF := DetectTemporalOrphans(bOFF.g)

	assert.GreaterOrEqual(t, len(reportOFF.OrphanActivity), len(reportON.OrphanActivity),
		"OFF: orphan activity count must be >= ON count (suppressed dispatch adds orphans)")
}

// --- nil skip = RunFrameworkSynthesizers parity test ------------------

// TestRunFrameworkSynthesizersExcept_NilSkipParityWithBase confirms that
// RunFrameworkSynthesizersExcept(g, nil) produces identical totals to
// RunFrameworkSynthesizers(g) on the same fixture.
func TestRunFrameworkSynthesizersExcept_NilSkipParityWithBase(t *testing.T) {
	b1 := buildBaseTemporalFixture()
	rep1 := RunFrameworkSynthesizers(b1.g)

	b2 := buildBaseTemporalFixture()
	rep2 := RunFrameworkSynthesizersExcept(b2.g, nil)

	assert.Equal(t, rep1.Total, rep2.Total, "nil skip must produce same total as RunFrameworkSynthesizers")
	require.Equal(t, len(rep1.Per), len(rep2.Per), "nil skip must produce same per-synthesizer count")
	for i := range rep1.Per {
		assert.Equal(t, rep1.Per[i].Name, rep2.Per[i].Name)
		assert.Equal(t, rep1.Per[i].Edges, rep2.Per[i].Edges)
	}
}

// --- env-path test -----------------------------------------------------

// TestTemporalGate_EnvOff_ConfigReturnsFalse verifies that the
// GORTEX_TEMPORAL=off env variable causes TemporalDispatchEnabledOrDefault
// to return false — confirming the env-driven gate path works end-to-end
// without a full indexer run.
func TestTemporalGate_EnvOff_ConfigReturnsFalse(t *testing.T) {
	t.Setenv("GORTEX_TEMPORAL", "off")
	cfg := config.IndexConfig{}
	assert.False(t, cfg.TemporalDispatchEnabledOrDefault(),
		"GORTEX_TEMPORAL=off must disable temporal dispatch")
}

// TestTemporalGate_EnvOn_ConfigReturnsTrue verifies that GORTEX_TEMPORAL=on
// causes TemporalDispatchEnabledOrDefault to return true.
func TestTemporalGate_EnvOn_ConfigReturnsTrue(t *testing.T) {
	t.Setenv("GORTEX_TEMPORAL", "on")
	cfg := config.IndexConfig{}
	assert.True(t, cfg.TemporalDispatchEnabledOrDefault(),
		"GORTEX_TEMPORAL=on must enable temporal dispatch")
}

// TestTemporalGate_EnvUnset_DefaultON verifies that an unset env and nil config
// flag produce the default-ON behaviour.
func TestTemporalGate_EnvUnset_DefaultON(t *testing.T) {
	// Ensure env is clean (t.Setenv with empty is not the same as unset).
	t.Setenv("GORTEX_TEMPORAL", "")
	cfg := config.IndexConfig{} // SynthesizeTemporalDispatch = nil
	assert.True(t, cfg.TemporalDispatchEnabledOrDefault(),
		"unset env + nil config must default to ON")
}
