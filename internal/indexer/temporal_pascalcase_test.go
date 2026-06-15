package indexer

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

// PURPOSE — Cat 4 (PascalCase exact-name fallback): a dispatch whose name is a
// concrete PascalCase identifier that exactly matches an unregistered,
// non-suffixed Go function resolves ONLY when that function's signature
// matches the dispatch kind (activity→context.Context / workflow→
// workflow.Context) and the match is unique. Signature / kind mismatches must
// abstain — precision over recall (the edge is speculative + hidden).
//
// KEYWORDS — temporal, PascalCase, exact-name, signature-gate, Cat4

func TestTemporalE2E_PascalCaseExactSig_Resolves(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

func OrderWorkflow(ctx workflow.Context) error {
	return workflow.ExecuteChildWorkflow(ctx, ProcessPriceChange, nil).Get(ctx, nil)
}

func ProcessPriceChange(ctx workflow.Context) error { return nil }
`)
	writeFile(t, filepath.Join(dir, "main.go"), `package wf

func setupWorker(w Worker) {
	w.RegisterWorkflow(OrderWorkflow)
}
`)
	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	target := g.FindNodesByName("ProcessPriceChange")
	require.Len(t, target, 1)
	stub := stubEdgeFrom(t, g, "OrderWorkflow")
	assert.Equal(t, target[0].ID, stub.To,
		"exact-name workflow-shaped function must resolve via exact_sig")
	assert.Equal(t, "exact_sig", stub.Meta["temporal_resolution_via"])
	_, hidden := stub.Meta[graph.MetaSpeculative]
	assert.True(t, hidden, "exact_sig resolution is speculative/hidden")
}

func TestTemporalE2E_PascalCaseExactSig_SignatureMismatchAbstains(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

func OrderWorkflow(ctx workflow.Context) error {
	return workflow.ExecuteActivity(ctx, ComputeTotals, nil).Get(ctx, nil)
}

func ComputeTotals(in int) int { return in }
`)
	writeFile(t, filepath.Join(dir, "main.go"), `package wf

func setupWorker(w Worker) {
	w.RegisterWorkflow(OrderWorkflow)
}
`)
	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	stub := stubEdgeFrom(t, g, "OrderWorkflow")
	assert.Contains(t, stub.To, "unresolved::temporal::",
		"a non-Temporal-shaped exact-name match must NOT resolve; got To=%s", stub.To)
}

func TestTemporalE2E_PascalCaseExactSig_KindMismatchAbstains(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

func OrderWorkflow(ctx workflow.Context) error {
	// activity dispatch onto a workflow-shaped function — kind mismatch.
	return workflow.ExecuteActivity(ctx, SettleInvoice, nil).Get(ctx, nil)
}

func SettleInvoice(ctx workflow.Context) error { return nil }
`)
	writeFile(t, filepath.Join(dir, "main.go"), `package wf

func setupWorker(w Worker) {
	w.RegisterWorkflow(OrderWorkflow)
}
`)
	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	stub := stubEdgeFrom(t, g, "OrderWorkflow")
	assert.Contains(t, stub.To, "unresolved::temporal::",
		"activity dispatch onto a workflow.Context function must abstain; got To=%s", stub.To)
}
