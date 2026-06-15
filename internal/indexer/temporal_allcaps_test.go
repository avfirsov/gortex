package indexer

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

// PURPOSE — Cat 3 (ALL_CAPS const): a dispatch named by an ALL_CAPS string
// constant (`workflow.ExecuteActivity(ctx, PROJECT_X_ACTIVITY, …)`) must
// resolve by dereferencing the constant's literal value via constVal —
// including the const-block form and one-hop const-to-const aliases.
//
// KEYWORDS — temporal, ALL_CAPS, const, constVal, Cat3

func allcapsAssertResolved(t *testing.T, dir, wfName, actName string) {
	t.Helper()
	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)
	act := g.FindNodesByName(actName)
	require.Len(t, act, 1, "activity %s must exist", actName)
	stub := stubEdgeFrom(t, g, wfName)
	assert.Equal(t, act[0].ID, stub.To,
		"ALL_CAPS const dispatch must resolve to %s; got To=%s", actName, stub.To)
}

func TestTemporalE2E_AllCapsConst_Direct(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

const PROJECT_VALIDATE_ACTIVITY = "ProjectValidateActivity"

func OrderWorkflow(ctx workflow.Context) error {
	return workflow.ExecuteActivity(ctx, PROJECT_VALIDATE_ACTIVITY, nil).Get(ctx, nil)
}
`)
	writeFile(t, filepath.Join(dir, "activity.go"), `package wf

import "context"

func ProjectValidateActivity(ctx context.Context) error { return nil }
`)
	writeFile(t, filepath.Join(dir, "main.go"), `package wf

func setupWorker(w Worker) {
	w.RegisterWorkflow(OrderWorkflow)
	w.RegisterActivity(ProjectValidateActivity)
}
`)
	allcapsAssertResolved(t, dir, "OrderWorkflow", "ProjectValidateActivity")
}

func TestTemporalE2E_AllCapsConst_Block(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

const (
	FOO_ACTIVITY = "FooActivity"
	BAR_ACTIVITY = "BarActivity"
)

func OrderWorkflow(ctx workflow.Context) error {
	return workflow.ExecuteActivity(ctx, BAR_ACTIVITY, nil).Get(ctx, nil)
}
`)
	writeFile(t, filepath.Join(dir, "activity.go"), `package wf

import "context"

func BarActivity(ctx context.Context) error { return nil }
`)
	writeFile(t, filepath.Join(dir, "main.go"), `package wf

func setupWorker(w Worker) {
	w.RegisterWorkflow(OrderWorkflow)
	w.RegisterActivity(BarActivity)
}
`)
	allcapsAssertResolved(t, dir, "OrderWorkflow", "BarActivity")
}

func TestTemporalE2E_AllCapsConst_ConstToConst(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

const RealChargeName = "ChargeActivity"
const ALIAS_CHARGE_ACTIVITY = RealChargeName

func OrderWorkflow(ctx workflow.Context) error {
	return workflow.ExecuteActivity(ctx, ALIAS_CHARGE_ACTIVITY, nil).Get(ctx, nil)
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
	allcapsAssertResolved(t, dir, "OrderWorkflow", "ChargeActivity")
}
