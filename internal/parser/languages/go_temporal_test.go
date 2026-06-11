package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

// temporalEdgesByVia returns every EdgeCalls edge tagged with the given
// `via` value (e.g. "temporal.stub" or "temporal.register").
func temporalEdgesByVia(fix *extractedFixture, via string) []*graph.Edge {
	var found []*graph.Edge
	for _, e := range fix.edgesByKind[graph.EdgeCalls] {
		if e.Meta != nil && e.Meta["via"] == via {
			found = append(found, e)
		}
	}
	return found
}

func TestGoTemporal_ExecuteActivity_IdentifierName(t *testing.T) {
	fix := runGoExtract(t, `package wf

import "go.temporal.io/sdk/workflow"

func OrderWorkflow(ctx workflow.Context, id string) error {
	workflow.ExecuteActivity(ctx, ChargeCard, id)
	return nil
}
`)
	edges := temporalEdgesByVia(fix, "temporal.stub")
	require.Len(t, edges, 1)
	e := edges[0]
	assert.Equal(t, "unresolved::temporal::activity::ChargeCard", e.To)
	assert.Equal(t, "activity", e.Meta["temporal_kind"])
	assert.Equal(t, "ChargeCard", e.Meta["temporal_name"])
	_, isLocal := e.Meta["temporal_local"]
	assert.False(t, isLocal, "ExecuteActivity must not flag temporal_local")
}

func TestGoTemporal_ExecuteActivity_StringLiteralName(t *testing.T) {
	fix := runGoExtract(t, `package wf

import "go.temporal.io/sdk/workflow"

func WF(ctx workflow.Context) {
	workflow.ExecuteActivity(ctx, "RemoteActivity", nil)
}
`)
	edges := temporalEdgesByVia(fix, "temporal.stub")
	require.Len(t, edges, 1)
	assert.Equal(t, "unresolved::temporal::activity::RemoteActivity", edges[0].To)
	assert.Equal(t, "RemoteActivity", edges[0].Meta["temporal_name"])
}

func TestGoTemporal_ExecuteActivity_SelectorName(t *testing.T) {
	// `workflow.ExecuteActivity(ctx, pkg.Charge, ...)` → name is "Charge"
	// (the trailing identifier of the selector).
	fix := runGoExtract(t, `package wf

import (
	"go.temporal.io/sdk/workflow"
	"example.com/activities"
)

func WF(ctx workflow.Context) {
	workflow.ExecuteActivity(ctx, activities.Charge, 1)
}
`)
	edges := temporalEdgesByVia(fix, "temporal.stub")
	require.Len(t, edges, 1)
	assert.Equal(t, "unresolved::temporal::activity::Charge", edges[0].To)
}

func TestGoTemporal_ExecuteLocalActivity_FlagsTemporalLocal(t *testing.T) {
	fix := runGoExtract(t, `package wf

import "go.temporal.io/sdk/workflow"

func WF(ctx workflow.Context) {
	workflow.ExecuteLocalActivity(ctx, Lookup, "k")
}
`)
	edges := temporalEdgesByVia(fix, "temporal.stub")
	require.Len(t, edges, 1)
	e := edges[0]
	assert.Equal(t, "activity", e.Meta["temporal_kind"])
	assert.Equal(t, true, e.Meta["temporal_local"], "ExecuteLocalActivity must flag temporal_local")
}

func TestGoTemporal_ExecuteChildWorkflow_KindIsWorkflow(t *testing.T) {
	fix := runGoExtract(t, `package wf

import "go.temporal.io/sdk/workflow"

func Parent(ctx workflow.Context) {
	workflow.ExecuteChildWorkflow(ctx, ChildWorkflow, 42)
}
`)
	edges := temporalEdgesByVia(fix, "temporal.stub")
	require.Len(t, edges, 1)
	assert.Equal(t, "unresolved::temporal::workflow::ChildWorkflow", edges[0].To)
	assert.Equal(t, "workflow", edges[0].Meta["temporal_kind"])
}

func TestGoTemporal_RegisterActivity(t *testing.T) {
	fix := runGoExtract(t, `package main

func setup(w Worker) {
	w.RegisterActivity(ChargeCard)
}
`)
	edges := temporalEdgesByVia(fix, "temporal.register")
	require.Len(t, edges, 1)
	e := edges[0]
	assert.Equal(t, "activity", e.Meta["temporal_kind"])
	assert.Equal(t, "ChargeCard", e.Meta["temporal_name"])
}

func TestGoTemporal_RegisterActivityWithOptions(t *testing.T) {
	fix := runGoExtract(t, `package main

import "go.temporal.io/sdk/activity"

func setup(w Worker) {
	w.RegisterActivityWithOptions(ChargeCard, activity.RegisterOptions{Name: "Charge"})
}
`)
	edges := temporalEdgesByVia(fix, "temporal.register")
	require.Len(t, edges, 1)
	assert.Equal(t, "activity", edges[0].Meta["temporal_kind"])
	assert.Equal(t, "ChargeCard", edges[0].Meta["temporal_name"])
}

func TestGoTemporal_RegisterWorkflow(t *testing.T) {
	fix := runGoExtract(t, `package main

func setup(w Worker) {
	w.RegisterWorkflow(OrderWorkflow)
}
`)
	edges := temporalEdgesByVia(fix, "temporal.register")
	require.Len(t, edges, 1)
	assert.Equal(t, "workflow", edges[0].Meta["temporal_kind"])
	assert.Equal(t, "OrderWorkflow", edges[0].Meta["temporal_name"])
}

func TestGoTemporal_OtherWorkflowMethodNotStubbed(t *testing.T) {
	// `workflow.Sleep` / `workflow.Now` / etc. must NOT be stamped as
	// temporal.stub — only the four explicit dispatch helpers are.
	fix := runGoExtract(t, `package wf

import "go.temporal.io/sdk/workflow"

func WF(ctx workflow.Context) {
	workflow.Sleep(ctx, 5)
	workflow.Now(ctx)
}
`)
	assert.Empty(t, temporalEdgesByVia(fix, "temporal.stub"),
		"only ExecuteActivity / ExecuteLocalActivity / ExecuteChildWorkflow should be stub-tagged")
}

func TestGoTemporal_AliasedImportNotDetected(t *testing.T) {
	// We require the receiver text to be exactly "workflow" — aliased
	// imports (intentionally) miss; this test pins that contract so a
	// future relaxation is a conscious decision.
	fix := runGoExtract(t, `package wf

import wf "go.temporal.io/sdk/workflow"

func WF(ctx wf.Context) {
	wf.ExecuteActivity(ctx, Charge, 1)
}
`)
	assert.Empty(t, temporalEdgesByVia(fix, "temporal.stub"))
}

func TestGoTemporal_StubAndRegisterCoexistInSameFile(t *testing.T) {
	fix := runGoExtract(t, `package main

import "go.temporal.io/sdk/workflow"

func Charge() error { return nil }

func WF(ctx workflow.Context) {
	workflow.ExecuteActivity(ctx, Charge, 1)
}

func setup(w Worker) {
	w.RegisterActivity(Charge)
	w.RegisterWorkflow(WF)
}
`)
	stubs := temporalEdgesByVia(fix, "temporal.stub")
	registers := temporalEdgesByVia(fix, "temporal.register")
	require.Len(t, stubs, 1)
	require.Len(t, registers, 2)
}

// --- In-workflow handler declarations (query / signal / update) -----
//
// These mirror the Java SDK's @QueryMethod / @SignalMethod /
// @UpdateMethod annotations: from inside a workflow body the Go SDK
// declares the named query / signal / update channels the workflow
// serves. We surface each as a `via=temporal.handler` EdgeCalls edge
// carrying temporal_kind + temporal_name so the graph records, per
// workflow, the named handlers it exposes — symmetric with the Java
// side's per-method annotation edges.

func TestGoTemporal_SetQueryHandler(t *testing.T) {
	fix := runGoExtract(t, `package wf

import "go.temporal.io/sdk/workflow"

func OrderWorkflow(ctx workflow.Context) error {
	workflow.SetQueryHandler(ctx, "status", func() (string, error) { return "ok", nil })
	return nil
}
`)
	edges := temporalEdgesByVia(fix, "temporal.handler")
	require.Len(t, edges, 1)
	e := edges[0]
	assert.Equal(t, "query", e.Meta["temporal_kind"])
	assert.Equal(t, "status", e.Meta["temporal_name"])
	assert.Equal(t, "pkg/foo.go::OrderWorkflow", e.From,
		"handler edge must originate from the enclosing workflow function")
}

func TestGoTemporal_GetSignalChannel(t *testing.T) {
	fix := runGoExtract(t, `package wf

import "go.temporal.io/sdk/workflow"

func OrderWorkflow(ctx workflow.Context) error {
	ch := workflow.GetSignalChannel(ctx, "cancel")
	_ = ch
	return nil
}
`)
	edges := temporalEdgesByVia(fix, "temporal.handler")
	require.Len(t, edges, 1)
	assert.Equal(t, "signal", edges[0].Meta["temporal_kind"])
	assert.Equal(t, "cancel", edges[0].Meta["temporal_name"])
}

func TestGoTemporal_SetUpdateHandler(t *testing.T) {
	fix := runGoExtract(t, `package wf

import "go.temporal.io/sdk/workflow"

func OrderWorkflow(ctx workflow.Context) error {
	workflow.SetUpdateHandler(ctx, "retry", func() error { return nil })
	return nil
}
`)
	edges := temporalEdgesByVia(fix, "temporal.handler")
	require.Len(t, edges, 1)
	assert.Equal(t, "update", edges[0].Meta["temporal_kind"])
	assert.Equal(t, "retry", edges[0].Meta["temporal_name"])
}

func TestGoTemporal_SetUpdateHandlerWithOptions(t *testing.T) {
	fix := runGoExtract(t, `package wf

import "go.temporal.io/sdk/workflow"

func OrderWorkflow(ctx workflow.Context) error {
	workflow.SetUpdateHandlerWithOptions(ctx, "retry", func() error { return nil }, workflow.UpdateHandlerOptions{})
	return nil
}
`)
	edges := temporalEdgesByVia(fix, "temporal.handler")
	require.Len(t, edges, 1)
	assert.Equal(t, "update", edges[0].Meta["temporal_kind"])
	assert.Equal(t, "retry", edges[0].Meta["temporal_name"])
}

func TestGoTemporal_HandlerNonLiteralNameUndetected(t *testing.T) {
	// Query / signal / update names are matched by string at runtime;
	// a non-literal name (variable / selector) can't be pinned here, so
	// no handler edge is emitted — high-precision, no guessing.
	fix := runGoExtract(t, `package wf

import "go.temporal.io/sdk/workflow"

func OrderWorkflow(ctx workflow.Context, q string) error {
	workflow.SetQueryHandler(ctx, q, func() (string, error) { return "ok", nil })
	return nil
}
`)
	assert.Empty(t, temporalEdgesByVia(fix, "temporal.handler"),
		"non-literal handler name must not be detected")
}

func TestGoTemporal_HandlerAliasedImportNotDetected(t *testing.T) {
	// Consistent with the dispatch detector: only the canonical
	// "workflow" receiver alias is recognised.
	fix := runGoExtract(t, `package wf

import wf "go.temporal.io/sdk/workflow"

func OrderWorkflow(ctx wf.Context) error {
	wf.SetQueryHandler(ctx, "status", func() (string, error) { return "ok", nil })
	return nil
}
`)
	assert.Empty(t, temporalEdgesByVia(fix, "temporal.handler"))
}
