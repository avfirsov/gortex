package indexer

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/resolver"
)

// PURPOSE: empirically settle whether cross-repo Temporal dispatch
// resolves in the daemon's merged-store mode. The fork's design doc
// (03-temporal-fork-next-phase.md) claims it does NOT, attributing the
// gap to a per-repo index — but every other Temporal E2E test is
// single-graph, so the claim was never tested. These tests reproduce
// the daemon shape (one shared graph.Store, per-repo indexers with
// RepoPrefix tags + deferred global passes, one settle) and assert the
// real behaviour.
//
// RATIONALE: the daemon's MultiIndexer indexes every repo into ONE
// graph.Store (multi.go) and runs the framework-synth passes (incl.
// ResolveTemporalCalls) once at the end over that merged store. Two
// Indexers sharing a single graph.New() with SetRepoPrefix +
// SetDeferGlobalPasses(true), followed by one RunGlobalGraphPasses, is
// a faithful, dependency-light proxy for that path.
//
// KEYWORDS: temporal, cross-repo, multi-repo, merged-store, resolver,
// ambiguity-abstention, const_ref

// indexRepoInto indexes one repo directory into the shared graph under a
// RepoPrefix, deferring the global derivation passes so the caller can
// run a single settle over the fully-populated multi-repo store —
// mirroring MultiIndexer.BeginBatch → per-repo index → RunGlobalGraphPasses.
func indexRepoInto(t *testing.T, g graph.Store, prefix, dir string) *Indexer {
	t.Helper()
	idx := newTestIndexer(g)
	idx.SetRepoPrefix(prefix)
	idx.SetDeferGlobalPasses(true)
	_, err := idx.IndexCtx(context.Background(), dir)
	require.NoError(t, err)
	return idx
}

// findStubEdge returns the lone outbound via=temporal.stub edge of a
// node, failing the test if absent.
func findStubEdge(t *testing.T, g graph.Store, fromID string) *graph.Edge {
	t.Helper()
	for _, e := range g.GetOutEdges(fromID) {
		if e != nil && e.Meta != nil && e.Meta["via"] == "temporal.stub" {
			return e
		}
	}
	require.FailNow(t, "no outbound temporal.stub edge", "from=%s", fromID)
	return nil
}

// TestTemporalMultiRepo_CrossRepoDispatchResolves — the positive case
// (plan Phase 0.1). A workflow in repo A dispatches "ChargeActivity";
// the activity is registered in repo B. Over the merged store the stub
// must land on repo B's activity at the register-confirmed tier.
func TestTemporalMultiRepo_CrossRepoDispatchResolves(t *testing.T) {
	g := graph.New()

	dirA := t.TempDir()
	writeFile(t, filepath.Join(dirA, "workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

func ChargeWorkflow(ctx workflow.Context) error {
	return workflow.ExecuteActivity(ctx, "ChargeActivity", nil).Get(ctx, nil)
}
`)
	dirB := t.TempDir()
	writeFile(t, filepath.Join(dirB, "activity.go"), `package act

import "context"

func ChargeActivity(ctx context.Context) error { return nil }

func setupWorker(w Worker) {
	w.RegisterActivity(ChargeActivity)
}
`)

	indexRepoInto(t, g, "repoA", dirA)
	settler := indexRepoInto(t, g, "repoB", dirB)
	settler.RunGlobalGraphPasses(context.Background())

	actNodes := g.FindNodesByName("ChargeActivity")
	require.Len(t, actNodes, 1)
	activity := actNodes[0]
	assert.Equal(t, "repoB", activity.RepoPrefix, "activity must be tagged with its repo")

	wf := g.FindNodesByName("ChargeWorkflow")
	require.Len(t, wf, 1)

	stub := findStubEdge(t, g, wf[0].ID)
	assert.Equal(t, activity.ID, stub.To,
		"cross-repo dispatch must land on repo B's registered activity")
	assert.Equal(t, graph.OriginASTResolved, stub.Origin,
		"register-confirmed cross-repo match is a resolved edge")
}

// TestTemporalMultiRepo_AmbiguousCrossRepoAbstains — the real limitation
// (plan Phase 0.2). The SAME activity name is registered in repo B AND
// repo C, with no same-repo candidate for the repo-A caller. lookup
// resolves only on a unique candidate, so it must ABSTAIN — the stub
// stays an unresolved placeholder. This documents that the cross-repo
// limit is ambiguity (precision by design), not a per-repo index.
func TestTemporalMultiRepo_AmbiguousCrossRepoAbstains(t *testing.T) {
	g := graph.New()

	dirA := t.TempDir()
	writeFile(t, filepath.Join(dirA, "workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

func ChargeWorkflow(ctx workflow.Context) error {
	return workflow.ExecuteActivity(ctx, "ChargeActivity", nil).Get(ctx, nil)
}
`)
	activitySrc := `package act

import "context"

func ChargeActivity(ctx context.Context) error { return nil }

func setupWorker(w Worker) {
	w.RegisterActivity(ChargeActivity)
}
`
	dirB := t.TempDir()
	writeFile(t, filepath.Join(dirB, "activity.go"), activitySrc)
	dirC := t.TempDir()
	writeFile(t, filepath.Join(dirC, "activity.go"), activitySrc)

	indexRepoInto(t, g, "repoA", dirA)
	indexRepoInto(t, g, "repoB", dirB)
	settler := indexRepoInto(t, g, "repoC", dirC)
	settler.RunGlobalGraphPasses(context.Background())

	wf := g.FindNodesByName("ChargeWorkflow")
	require.Len(t, wf, 1)

	stub := findStubEdge(t, g, wf[0].ID)
	assert.True(t, strings.HasPrefix(stub.To, "unresolved::temporal::"),
		"ambiguous cross-repo name must abstain, leaving the placeholder; got To=%s", stub.To)
	assert.NotEqual(t, graph.OriginASTResolved, stub.Origin,
		"an abstained stub is not a resolved edge")
}

// TestTemporalMultiRepo_CrossRepoConstNameResolves — the cross-repo
// const case the ТЗ wrongly filed under "Fix B" (plan Phase 0.3). The
// repo-A workflow dispatches via the const name ENS_BILLING_ACTIVITY;
// both the const (value "BillingActivity") and the registered activity
// live in repo B. Resolution must go dispatch-name → cross-repo constVal
// → cross-repo registered handler.
func TestTemporalMultiRepo_CrossRepoConstNameResolves(t *testing.T) {
	g := graph.New()

	dirA := t.TempDir()
	writeFile(t, filepath.Join(dirA, "workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

func BillingWorkflow(ctx workflow.Context) error {
	return workflow.ExecuteActivity(ctx, ENS_BILLING_ACTIVITY, nil).Get(ctx, nil)
}
`)
	dirB := t.TempDir()
	writeFile(t, filepath.Join(dirB, "activity.go"), `package act

import "context"

const ENS_BILLING_ACTIVITY = "BillingActivity"

func BillingActivity(ctx context.Context) error { return nil }

func setupWorker(w Worker) {
	w.RegisterActivity(BillingActivity)
}
`)

	indexRepoInto(t, g, "repoA", dirA)
	settler := indexRepoInto(t, g, "repoB", dirB)
	settler.RunGlobalGraphPasses(context.Background())

	actNodes := g.FindNodesByName("BillingActivity")
	require.Len(t, actNodes, 1)
	activity := actNodes[0]

	wf := g.FindNodesByName("BillingWorkflow")
	require.Len(t, wf, 1)

	stub := findStubEdge(t, g, wf[0].ID)
	assert.Equal(t, activity.ID, stub.To,
		"cross-repo const-named dispatch must resolve via cross-repo constVal + handler lookup")
	assert.Equal(t, "BillingActivity", stub.Meta["temporal_const_value"],
		"the resolved const literal must be recorded on the edge")
}

// TestTemporalMultiRepo_RepoAffinityConstResolves — the repo-affinity const
// case. The const name CALCULATE_ACTIVITY has value "CalculateActivity" in
// repo B and "ValidateCalculateActivity" in repo C. The global constVal
// drops it (ambiguous). But a dispatch from repo B should resolve via
// constValByRepo["repoB::CALCULATE_ACTIVITY"] → "CalculateActivity" → the
// registered handler in repo B.
func TestTemporalMultiRepo_RepoAffinityConstResolves(t *testing.T) {
	g := graph.New()

	// Repo B: defines CALCULATE_ACTIVITY = "CalculateActivity", registers
	// the activity, AND contains a workflow that dispatches via the same
	// const. All in one repo so repo-affinity finds the const under
	// "repoB::CALCULATE_ACTIVITY".
	dirB := t.TempDir()
	writeFile(t, filepath.Join(dirB, "activity.go"), `package act

import "context"

const CALCULATE_ACTIVITY = "CalculateActivity"

func CalculateActivity(ctx context.Context) error { return nil }

func setupWorker(w Worker) {
	w.RegisterActivity(CalculateActivity)
}
`)
	writeFile(t, filepath.Join(dirB, "workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

func LocalWorkflow(ctx workflow.Context) error {
	return workflow.ExecuteActivity(ctx, CALCULATE_ACTIVITY, nil).Get(ctx, nil)
}
`)

	// Repo C: defines CALCULATE_ACTIVITY = "ValidateCalculateActivity" + registers THAT.
	// Same const name, different value → global constVal drops the name.
	dirC := t.TempDir()
	writeFile(t, filepath.Join(dirC, "activity.go"), `package act

import "context"

const CALCULATE_ACTIVITY = "ValidateCalculateActivity"

func ValidateCalculateActivity(ctx context.Context) error { return nil }

func setupWorker(w Worker) {
	w.RegisterActivity(ValidateCalculateActivity)
}
`)

	indexRepoInto(t, g, "repoB", dirB)
	settler := indexRepoInto(t, g, "repoC", dirC)
	settler.RunGlobalGraphPasses(context.Background())

	// Find the LocalWorkflow node in repoB
	wf := g.FindNodesByName("LocalWorkflow")
	require.Len(t, wf, 1)

	stub := findStubEdge(t, g, wf[0].ID)
	assert.NotEqual(t, "", stub.Meta["temporal_const_value"],
		"repo-affinity const resolution should stamp temporal_const_value on the edge")

	// Verify the dispatch actually resolved to repo B's activity
	actNodes := g.FindNodesByName("CalculateActivity")
	require.Len(t, actNodes, 1)
	assert.Equal(t, actNodes[0].ID, stub.To,
		"repo-affinity must resolve dispatch from repoB to repoB's registered activity")
}

// TestTemporalMultiRepo_SelectorConstResolves — the real ACME pattern:
// a workflow dispatches via `constants.GetFooActivityName` (selector_expression),
// and the const in a SEPARATE file defines
//
//	const GetFooActivityName = "FooActivity"
//
// The parser must extract the trailing identifier "GetFooActivityName",
// the resolver must find it in constVal, dereference to "FooActivity",
// and then find the registered handler.
func TestTemporalMultiRepo_SelectorConstResolves(t *testing.T) {
	g := graph.New()

	// Repo with the activity implementation + worker registration
	dirA := t.TempDir()
	writeFile(t, filepath.Join(dirA, "activity.go"), `package act

import "context"

func FooActivity(ctx context.Context) error { return nil }

func setupWorker(w Worker) {
	w.RegisterActivity(FooActivity)
}
`)

	// Repo with the workflow + constants package
	dirB := t.TempDir()
	writeFile(t, filepath.Join(dirB, "constants.go"), `package constants

const GetFooActivityName = "FooActivity"
`)
	writeFile(t, filepath.Join(dirB, "workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

func MyWorkflow(ctx workflow.Context) error {
	return workflow.ExecuteActivity(ctx, constants.GetFooActivityName, nil).Get(ctx, nil)
}
`)

	indexRepoInto(t, g, "repoA", dirA)
	settler := indexRepoInto(t, g, "repoB", dirB)
	settler.RunGlobalGraphPasses(context.Background())

	wf := g.FindNodesByName("MyWorkflow")
	require.Len(t, wf, 1)

	stub := findStubEdge(t, g, wf[0].ID)

	// The dispatch should resolve through const dereferencing:
	// "GetFooActivityName" → constVal → "FooActivity" → registered handler
	actNodes := g.FindNodesByName("FooActivity")
	require.Len(t, actNodes, 1)
	assert.Equal(t, actNodes[0].ID, stub.To,
		"selector-expression const dispatch must resolve via constVal dereferencing")
	assert.Equal(t, "FooActivity", stub.Meta["temporal_const_value"],
		"the resolved const literal must be recorded on the edge")
}

// TestTemporalMultiRepo_ParenConstBlockResolves — same as SelectorConst but
// the constants live in a parenthesised const block (the real ACME pattern
// in constants/activities.go). Tree-sitter represents `const (...)` as
// multiple const_spec children under a const_declaration; each must be
// indexed into constVal independently.
func TestTemporalMultiRepo_ParenConstBlockResolves(t *testing.T) {
	g := graph.New()

	dirA := t.TempDir()
	writeFile(t, filepath.Join(dirA, "activity.go"), `package act

import "context"

func FooActivity(ctx context.Context) error { return nil }
func BarActivity(ctx context.Context) error { return nil }

func setupWorker(w Worker) {
	w.RegisterActivity(FooActivity)
	w.RegisterActivity(BarActivity)
}
`)

	dirB := t.TempDir()
	writeFile(t, filepath.Join(dirB, "constants.go"), `package constants

const (
	GetFooActivityName = "FooActivity"
	GetFooActivityQueue = "foo-queue"
	GetBarActivityName = "BarActivity"
	GetBarActivityQueue = "bar-queue"
)
`)
	writeFile(t, filepath.Join(dirB, "workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

func MyWorkflow(ctx workflow.Context) error {
	workflow.ExecuteActivity(ctx, constants.GetFooActivityName, nil).Get(ctx, nil)
	return workflow.ExecuteActivity(ctx, constants.GetBarActivityName, nil).Get(ctx, nil)
}
`)

	indexRepoInto(t, g, "repoA", dirA)
	settler := indexRepoInto(t, g, "repoB", dirB)
	settler.RunGlobalGraphPasses(context.Background())

	wf := g.FindNodesByName("MyWorkflow")
	require.Len(t, wf, 1)

	// Both dispatches must resolve through const dereferencing
	stubs := g.GetOutEdges(wf[0].ID)
	resolved := 0
	for _, e := range stubs {
		if e.Meta != nil && e.Meta["via"] == "temporal.stub" {
			if cv, _ := e.Meta["temporal_const_value"].(string); cv != "" {
				resolved++
			}
		}
	}
	assert.Equal(t, 2, resolved,
		"both selector-expression const dispatches from parenthesised const block must resolve")
}

// TestTemporalMultiRepo_ConventionSignatureTiebreaker — the ACME pattern where
// the same function name exists as both a workflow wrapper (workflow.Context)
// and a real activity (context.Context). When the dispatch kind is "activity",
// the convention resolver must prefer the context.Context candidate.
func TestTemporalMultiRepo_ConventionSignatureTiebreaker(t *testing.T) {
	g := graph.New()

	// Repo A: workflow wrapper — same name as activity but takes workflow.Context.
	// This is a Temporal anti-pattern but common in ACME: the workflow file
	// defines a helper with the same name as the target activity.
	dirA := t.TempDir()
	writeFile(t, filepath.Join(dirA, "workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

func FooActivity(ctx workflow.Context, in string) (string, error) {
	// This is a workflow that dispatches the real activity by the same name.
	return "", workflow.ExecuteActivity(ctx, "FooActivity", in).Get(ctx, nil)
}
`)

	// Repo B: real activity — takes context.Context.
	dirB := t.TempDir()
	writeFile(t, filepath.Join(dirB, "activity.go"), `package act

import "context"

func FooActivity(ctx context.Context, in string) (string, error) {
	return "done", nil
}
`)

	// Repo C: workflow that dispatches FooActivity by name.
	dirC := t.TempDir()
	writeFile(t, filepath.Join(dirC, "caller.go"), `package caller

import "go.temporal.io/sdk/workflow"

func CallerWorkflow(ctx workflow.Context) error {
	return workflow.ExecuteActivity(ctx, "FooActivity", nil).Get(ctx, nil)
}
`)

	indexRepoInto(t, g, "repoA", dirA)
	indexRepoInto(t, g, "repoB", dirB)
	settler := indexRepoInto(t, g, "repoC", dirC)
	settler.RunGlobalGraphPasses(context.Background())

	wf := g.FindNodesByName("CallerWorkflow")
	require.Len(t, wf, 1)

	stub := findStubEdge(t, g, wf[0].ID)

	// The dispatch must resolve to repo B's real activity (context.Context),
	// not repo A's workflow wrapper (workflow.Context).
	realAct := g.FindNodesByName("FooActivity")
	require.GreaterOrEqual(t, len(realAct), 2, "expect at least 2 FooActivity nodes")

	// Find the one from repoB
	var realActivityID string
	for _, n := range realAct {
		if n.RepoPrefix == "repoB" {
			realActivityID = n.ID
			break
		}
	}
	require.NotEmpty(t, realActivityID, "must find FooActivity from repoB")

	assert.Equal(t, realActivityID, stub.To,
		"convention tiebreaker must prefer context.Context (real activity) over workflow.Context (wrapper)")
}

// TestTemporalMultiRepo_ConventionSingleMismatchFallback verifies the
// single-candidate signature-mismatch fallback: when convention finds
// exactly 1 candidate and its signature contradicts the dispatch kind
// (e.g., kind="activity" but candidate takes workflow.Context), the
// resolver accepts it at a lowered speculative confidence with
// temporal_resolution_via=convention_mismatch instead of rejecting.
func TestTemporalMultiRepo_ConventionSingleMismatchFallback(t *testing.T) {
	g := graph.New()

	// Repo A: workflow wrapper named "FooActivity" (workflow.Context).
	// No real activity with RegisterActivity — only convention matches.
	dirA := t.TempDir()
	writeFile(t, filepath.Join(dirA, "executor.go"), `package wf

import "go.temporal.io/sdk/workflow"

func FooActivity(ctx workflow.Context, in string) (string, error) {
	return "", workflow.ExecuteActivity(ctx, "FooActivity", in).Get(ctx, nil)
}
`)

	// Repo B: dispatches "FooActivity" — only convention candidate is
	// repo A's wrapper (workflow.Context, not context.Context).
	dirB := t.TempDir()
	writeFile(t, filepath.Join(dirB, "caller.go"), `package caller

import "go.temporal.io/sdk/workflow"

func CallerWorkflow(ctx workflow.Context) error {
	return workflow.ExecuteActivity(ctx, "FooActivity", nil).Get(ctx, nil)
}
`)

	indexRepoInto(t, g, "repoA", dirA)
	settler := indexRepoInto(t, g, "repoB", dirB)
	settler.RunGlobalGraphPasses(context.Background())

	wf := g.FindNodesByName("CallerWorkflow")
	require.Len(t, wf, 1)

	stub := findStubEdge(t, g, wf[0].ID)

	// The dispatch must resolve to repo A's wrapper at lowered confidence
	// (convention_mismatch), NOT stay as a broken placeholder.
	wrapper := g.FindNodesByName("FooActivity")
	require.GreaterOrEqual(t, len(wrapper), 1)
	assert.NotEqual(t, "", stub.To, "stub must be resolved (even at lowered confidence)")
	assert.Contains(t, stub.To, "FooActivity",
		"resolved target must be the convention-named wrapper")

	// Verify the mismatch metadata.
	via, _ := stub.Meta["temporal_resolution_via"].(string)
	assert.Equal(t, "convention_mismatch", via,
		"single-candidate with mismatching signature must be tagged convention_mismatch")
	assert.True(t, stub.Confidence <= 0.5,
		"convention_mismatch must land at speculative tier (confidence <= 0.5), got %.2f", stub.Confidence)
}

// TestTemporalMultiRepo_TestWorkflowFileSuppression verifies that dispatch
// edges originating from files matching *_test_*.go are suppressed from
// the broken_dispatch report, even when the file name does not follow the
// standard Go _test.go convention.
func TestTemporalMultiRepo_TestWorkflowFileSuppression(t *testing.T) {
	g := graph.New()

	// Repo with a test-workflow file that dispatches an activity.
	// The file is named "foo_test_workflow.go" (not "foo_test.go").
	dirA := t.TempDir()
	writeFile(t, filepath.Join(dirA, "foo_test_workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

func FooTestWorkflow(ctx workflow.Context) error {
	return workflow.ExecuteActivity(ctx, "MissingActivity", nil).Get(ctx, nil)
}
`)

	indexRepoInto(t, g, "repoA", dirA)
	settler := newTestIndexer(g)
	settler.SetRepoPrefix("repoA")
	settler.SetDeferGlobalPasses(true)
	_, err := settler.IndexCtx(context.Background(), dirA)
	require.NoError(t, err)
	settler.RunGlobalGraphPasses(context.Background())

	// The orphan detector must suppress this dispatch from broken_dispatch
	// because the source file matches *_test_*.go.
	rep := resolver.DetectTemporalOrphans(g)
	for _, bd := range rep.BrokenDispatch {
		assert.NotContains(t, bd.File, "_test_",
			"dispatch from *_test_*.go must be suppressed from broken_dispatch, got: %s", bd.File)
	}
}
