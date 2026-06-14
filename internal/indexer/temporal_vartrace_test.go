package indexer

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

// PURPOSE — Cat 2 (variable tracing): a Temporal dispatch whose name is a
// bare LOCAL VARIABLE (`name := …; workflow.ExecuteActivity(ctx, name, …)`)
// must resolve by tracing the variable's intra-procedural assignment to a
// string literal, a constant reference, or a const-returning func call.
// Without it the dispatch lands as broken_dispatch with temporal_name = the
// variable name (the "meta_vars" category: activity / activityName / type).
//
// RATIONALE — the parser already traces the env-var-with-default shape; this
// extends tracing to the plain literal/const/func assignment. Resolution is
// precision-safe: const/func names are validated against the resolver's
// constVal index (a non-const var simply fails to resolve and stays broken).
//
// KEYWORDS — temporal, variable-tracing, meta_vars, dispatch, Cat2

// stubEdgeFrom returns the lone outbound temporal.stub edge of the named node.
func stubEdgeFrom(t *testing.T, g graph.Store, name string) *graph.Edge {
	t.Helper()
	ns := g.FindNodesByName(name)
	require.NotEmpty(t, ns, "node %s must exist", name)
	for _, e := range g.GetOutEdges(ns[0].ID) {
		if e != nil && e.Meta != nil && e.Meta["via"] == "temporal.stub" {
			return e
		}
	}
	require.FailNow(t, "no temporal.stub edge", "from %s", name)
	return nil
}

// TestTemporalE2E_VarTrace_Literal — `act := "ChargeActivity"` then dispatch.
func TestTemporalE2E_VarTrace_Literal(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

func OrderWorkflow(ctx workflow.Context) error {
	act := "ChargeActivity"
	return workflow.ExecuteActivity(ctx, act, nil).Get(ctx, nil)
}
`)
	writeFile(t, filepath.Join(dir, "activity.go"), `package wf

import "context"

func ChargeActivity(ctx context.Context) error { return nil }
`)
	writeFile(t, filepath.Join(dir, "main.go"), `package wf

func setupWorker(w Worker) {
	w.RegisterWorkflow(OrderWorkflow)
	w.RegisterActivity(ChargeActivity)
}
`)
	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	activity := g.FindNodesByName("ChargeActivity")
	require.Len(t, activity, 1)
	stub := stubEdgeFrom(t, g, "OrderWorkflow")
	assert.Equal(t, activity[0].ID, stub.To,
		"literal-assigned dispatch var must resolve to the registered activity")
}

// TestTemporalE2E_VarTrace_Const — `act := chargeName` where chargeName is a
// string const; resolver derefs via constVal.
func TestTemporalE2E_VarTrace_Const(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

const chargeName = "ChargeActivity"

func OrderWorkflow(ctx workflow.Context) error {
	act := chargeName
	return workflow.ExecuteActivity(ctx, act, nil).Get(ctx, nil)
}
`)
	writeFile(t, filepath.Join(dir, "activity.go"), `package wf

import "context"

func ChargeActivity(ctx context.Context) error { return nil }
`)
	writeFile(t, filepath.Join(dir, "main.go"), `package wf

func setupWorker(w Worker) {
	w.RegisterWorkflow(OrderWorkflow)
	w.RegisterActivity(ChargeActivity)
}
`)
	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	activity := g.FindNodesByName("ChargeActivity")
	require.Len(t, activity, 1)
	stub := stubEdgeFrom(t, g, "OrderWorkflow")
	assert.Equal(t, activity[0].ID, stub.To,
		"const-assigned dispatch var must resolve via constVal")
	assert.Equal(t, "ChargeActivity", stub.Meta["temporal_const_value"])
}

// TestTemporalE2E_VarTrace_FuncReturn — `act := GetChargeName()` where the
// func unconditionally returns a string literal.
func TestTemporalE2E_VarTrace_FuncReturn(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

func GetChargeName() string { return "ChargeActivity" }

func OrderWorkflow(ctx workflow.Context) error {
	act := GetChargeName()
	return workflow.ExecuteActivity(ctx, act, nil).Get(ctx, nil)
}
`)
	writeFile(t, filepath.Join(dir, "activity.go"), `package wf

import "context"

func ChargeActivity(ctx context.Context) error { return nil }
`)
	writeFile(t, filepath.Join(dir, "main.go"), `package wf

func setupWorker(w Worker) {
	w.RegisterWorkflow(OrderWorkflow)
	w.RegisterActivity(ChargeActivity)
}
`)
	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	activity := g.FindNodesByName("ChargeActivity")
	require.Len(t, activity, 1)
	stub := stubEdgeFrom(t, g, "OrderWorkflow")
	assert.Equal(t, activity[0].ID, stub.To,
		"func-returning-literal-assigned dispatch var must resolve")
}

// TestTemporalE2E_VarTrace_UntraceableStaysBroken — precision guard: a var
// whose value can't be statically traced to a known name must NOT resolve.
func TestTemporalE2E_VarTrace_UntraceableStaysBroken(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "workflow.go"), `package wf

import (
	"os"

	"go.temporal.io/sdk/workflow"
)

func OrderWorkflow(ctx workflow.Context, in map[string]string) error {
	act := in["dynamic"]
	_ = os.Getenv
	return workflow.ExecuteActivity(ctx, act, nil).Get(ctx, nil)
}
`)
	writeFile(t, filepath.Join(dir, "activity.go"), `package wf

import "context"

func ChargeActivity(ctx context.Context) error { return nil }
`)
	writeFile(t, filepath.Join(dir, "main.go"), `package wf

func setupWorker(w Worker) {
	w.RegisterWorkflow(OrderWorkflow)
	w.RegisterActivity(ChargeActivity)
}
`)
	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	stub := stubEdgeFrom(t, g, "OrderWorkflow")
	assert.True(t, len(stub.To) > len("unresolved::temporal::") &&
		stub.To[:len("unresolved::temporal::")] == "unresolved::temporal::",
		"a non-statically-traceable dispatch var must stay unresolved; got To=%s", stub.To)
}
