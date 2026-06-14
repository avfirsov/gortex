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

// --- Dispatch name from an env-var-with-literal-default variable -----
//
// When the activity / workflow name is a local variable read from an
// env var with a literal fallback, resolve to the literal default and
// flag the stub edge `temporal_name_origin=env_default` so the resolver
// lands it at the speculative tier (the runtime env override may differ
// from the default). Anchored on a literal os.Getenv / os.LookupEnv read
// so the value is provably env-sourced — no general data-flow guessing.

func TestGoTemporal_ExecuteActivity_EnvDefault_CmpOr(t *testing.T) {
	fix := runGoExtract(t, `package wf

import (
	"cmp"
	"os"
	"go.temporal.io/sdk/workflow"
)

func WF(ctx workflow.Context) {
	actName := cmp.Or(os.Getenv("CHARGE_ACTIVITY"), "ChargeCard")
	workflow.ExecuteActivity(ctx, actName, 1)
}
`)
	edges := temporalEdgesByVia(fix, "temporal.stub")
	require.Len(t, edges, 1)
	e := edges[0]
	assert.Equal(t, "unresolved::temporal::activity::ChargeCard", e.To,
		"name must resolve to the literal default, not the variable identifier")
	assert.Equal(t, "ChargeCard", e.Meta["temporal_name"])
	assert.Equal(t, "env_default", e.Meta["temporal_name_origin"])
}

func TestGoTemporal_ExecuteActivity_EnvDefault_IfEmpty(t *testing.T) {
	fix := runGoExtract(t, `package wf

import (
	"os"
	"go.temporal.io/sdk/workflow"
)

func WF(ctx workflow.Context) {
	name := os.Getenv("CHARGE_ACTIVITY")
	if name == "" {
		name = "ChargeCard"
	}
	workflow.ExecuteActivity(ctx, name, 1)
}
`)
	edges := temporalEdgesByVia(fix, "temporal.stub")
	require.Len(t, edges, 1)
	assert.Equal(t, "unresolved::temporal::activity::ChargeCard", edges[0].To)
	assert.Equal(t, "ChargeCard", edges[0].Meta["temporal_name"])
	assert.Equal(t, "env_default", edges[0].Meta["temporal_name_origin"])
}

func TestGoTemporal_ExecuteActivity_PlainVarNotEnvDefault(t *testing.T) {
	// A variable NOT sourced from an env read keeps the existing
	// behaviour (trailing identifier as the name) and carries no
	// env_default flag — we don't guess at arbitrary variables.
	fix := runGoExtract(t, `package wf

import "go.temporal.io/sdk/workflow"

func WF(ctx workflow.Context, picked string) {
	actName := picked
	workflow.ExecuteActivity(ctx, actName, 1)
}
`)
	edges := temporalEdgesByVia(fix, "temporal.stub")
	require.Len(t, edges, 1)
	assert.Equal(t, "actName", edges[0].Meta["temporal_name"])
	_, flagged := edges[0].Meta["temporal_name_origin"]
	assert.False(t, flagged, "plain variable must not be flagged env_default")
}

func TestGoTemporal_ExecuteActivity_EnvReadNoLiteralDefault(t *testing.T) {
	// os.Getenv with no literal fallback can't be pinned to a name —
	// keep the variable identifier, no env_default flag.
	fix := runGoExtract(t, `package wf

import (
	"os"
	"go.temporal.io/sdk/workflow"
)

func WF(ctx workflow.Context) {
	name := os.Getenv("CHARGE_ACTIVITY")
	workflow.ExecuteActivity(ctx, name, 1)
}
`)
	edges := temporalEdgesByVia(fix, "temporal.stub")
	require.Len(t, edges, 1)
	assert.Equal(t, "name", edges[0].Meta["temporal_name"])
	_, flagged := edges[0].Meta["temporal_name_origin"]
	assert.False(t, flagged)
}

// G1 (env-with-fallback via helper calls): the dispatch name is assigned
// from a project-local env-or-default helper (e.g. wfutils.GetEnvOrDefault)
// rather than os.Getenv directly. The helper body lives in another package
// and is invisible at extract time, so the match is on the helper NAME
// only; the string-literal 2nd argument is taken as the fallback default
// and the stub is flagged env_default (speculative tier).

func TestEnvFallbackViaHelper_GetEnvOrDefault(t *testing.T) {
	fix := runGoExtract(t, `package wf

import (
	"go.temporal.io/sdk/workflow"
	"example.com/app/wfutils"
)

func WF(ctx workflow.Context) {
	actName := wfutils.GetEnvOrDefault("CHARGE_ACTIVITY", "ChargeActivity")
	workflow.ExecuteActivity(ctx, actName, 1)
}
`)
	edges := temporalEdgesByVia(fix, "temporal.stub")
	require.Len(t, edges, 1)
	e := edges[0]
	assert.Equal(t, "unresolved::temporal::activity::ChargeActivity", e.To,
		"helper env-default must resolve to the literal default, not the variable")
	assert.Equal(t, "ChargeActivity", e.Meta["temporal_name"])
	assert.Equal(t, "env_default", e.Meta["temporal_name_origin"])
}

func TestEnvFallbackViaHelper_GetEnvOrDefaultValue(t *testing.T) {
	// The `...Value`-suffixed sibling of GetEnvOrDefault is a real variant
	// seen in production env-helper packages (L1 corpus audit); it must be
	// recognised exactly like GetEnvOrDefault since the allow-list matches
	// the full callee name, not a substring.
	fix := runGoExtract(t, `package wf

import (
	"go.temporal.io/sdk/workflow"
	"example.com/app/envhelper"
)

func WF(ctx workflow.Context) {
	actName := envhelper.GetEnvOrDefaultValue("PROCESS_CANCEL_ACTIVITY", "ProcessCancelActivity")
	workflow.ExecuteActivity(ctx, actName, 1)
}
`)
	edges := temporalEdgesByVia(fix, "temporal.stub")
	require.Len(t, edges, 1)
	e := edges[0]
	assert.Equal(t, "ProcessCancelActivity", e.Meta["temporal_name"])
	assert.Equal(t, "env_default", e.Meta["temporal_name_origin"])
}

func TestEnvFallbackViaHelper_BareIdentifierEnvOr(t *testing.T) {
	// Bare (un-qualified) helper call: `EnvOr(KEY, "Default")`.
	fix := runGoExtract(t, `package wf

import "go.temporal.io/sdk/workflow"

func WF(ctx workflow.Context) {
	actName := EnvOr("REFUND_ACTIVITY", "RefundActivity")
	workflow.ExecuteActivity(ctx, actName, 1)
}
`)
	edges := temporalEdgesByVia(fix, "temporal.stub")
	require.Len(t, edges, 1)
	e := edges[0]
	assert.Equal(t, "RefundActivity", e.Meta["temporal_name"])
	assert.Equal(t, "env_default", e.Meta["temporal_name_origin"])
}

func TestEnvFallbackHeuristic_EnvNamedHelperFlaggedSpeculative(t *testing.T) {
	// Generic recall layer: a helper NOT in the allow-list but whose name
	// contains "env" (case-insensitive) is recognised by the structural
	// heuristic — its 2nd string-literal argument is taken as the default,
	// tagged temporal_env_source=heuristic so the resolver can keep it at the
	// hidden speculative tier (the LLM cleaning pass verifies it later).
	fix := runGoExtract(t, `package wf

import (
	"go.temporal.io/sdk/workflow"
	"example.com/app/cfg"
)

func WF(ctx workflow.Context) {
	actName := cfg.ActivityFromEnv("CHARGE_ACTIVITY", "ChargeActivity")
	workflow.ExecuteActivity(ctx, actName, 1)
}
`)
	edges := temporalEdgesByVia(fix, "temporal.stub")
	require.Len(t, edges, 1)
	e := edges[0]
	assert.Equal(t, "ChargeActivity", e.Meta["temporal_name"])
	assert.Equal(t, "env_default", e.Meta["temporal_name_origin"])
	assert.Equal(t, "heuristic", e.Meta["temporal_env_source"])
}

func TestEnvFallbackViaHelper_AllowlistTaggedSource(t *testing.T) {
	// An allow-list helper is tagged temporal_env_source=allowlist so the
	// resolver can promote it above the generic-heuristic tier.
	fix := runGoExtract(t, `package wf

import (
	"go.temporal.io/sdk/workflow"
	"example.com/app/wfutils"
)

func WF(ctx workflow.Context) {
	actName := wfutils.GetEnvOrDefault("CHARGE_ACTIVITY", "ChargeActivity")
	workflow.ExecuteActivity(ctx, actName, 1)
}
`)
	edges := temporalEdgesByVia(fix, "temporal.stub")
	require.Len(t, edges, 1)
	assert.Equal(t, "allowlist", edges[0].Meta["temporal_env_source"])
}

func TestEnvFallbackViaHelper_ConstSelectorDefault(t *testing.T) {
	// The #1 corpus gap: env-helper default is a selector_expression constant
	// reference, not a literal. temporal_name stays the dispatch variable; the
	// const NAME is recorded in temporal_default_const at the const_ref tier.
	fix := runGoExtract(t, `package wf

import (
	"go.temporal.io/sdk/workflow"
	"example.com/app/config"
	"example.com/app/wfutils"
)

func WF(ctx workflow.Context) {
	actName := wfutils.GetEnvOrDefault(config.ACTIVITY_NAME_ENV, config.ACTIVITY_NAME_DEFAULT)
	workflow.ExecuteActivity(ctx, actName, 1)
}
`)
	edges := temporalEdgesByVia(fix, "temporal.stub")
	require.Len(t, edges, 1)
	e := edges[0]
	assert.Equal(t, "actName", e.Meta["temporal_name"], "temporal_name stays the dispatch variable for a const default")
	assert.Equal(t, "ACTIVITY_NAME_DEFAULT", e.Meta["temporal_default_const"])
	assert.Equal(t, "env_default", e.Meta["temporal_name_origin"])
	assert.Equal(t, "const_ref", e.Meta["temporal_env_source"])
}

func TestEnvFallbackViaHelper_ConstBareDefault(t *testing.T) {
	// Local (un-qualified) constant default — a bare identifier.
	fix := runGoExtract(t, `package wf

import "go.temporal.io/sdk/workflow"

const VALIDATE_ACTIVITY_NAME_DEFAULT = "ValidateActivity"

func WF(ctx workflow.Context) {
	actName := EnvOr("VALIDATE_ACTIVITY_NAME_ENV", VALIDATE_ACTIVITY_NAME_DEFAULT)
	workflow.ExecuteActivity(ctx, actName, 1)
}
`)
	edges := temporalEdgesByVia(fix, "temporal.stub")
	require.Len(t, edges, 1)
	e := edges[0]
	assert.Equal(t, "VALIDATE_ACTIVITY_NAME_DEFAULT", e.Meta["temporal_default_const"])
	assert.Equal(t, "const_ref", e.Meta["temporal_env_source"])
}

func TestEnvFallbackHeuristic_ConstDefaultStaysHeuristic(t *testing.T) {
	// An env-NAMED (heuristic, not allow-listed) helper with a const default:
	// the const is recorded, but the source stays "heuristic" (the helper
	// itself is the unproven link → hidden speculative tier).
	fix := runGoExtract(t, `package wf

import (
	"go.temporal.io/sdk/workflow"
	"example.com/app/cfg"
)

func WF(ctx workflow.Context) {
	actName := cfg.ActivityFromEnv("CHARGE_ACTIVITY", cfg.CHARGE_ACTIVITY_DEFAULT)
	workflow.ExecuteActivity(ctx, actName, 1)
}
`)
	edges := temporalEdgesByVia(fix, "temporal.stub")
	require.Len(t, edges, 1)
	e := edges[0]
	assert.Equal(t, "CHARGE_ACTIVITY_DEFAULT", e.Meta["temporal_default_const"])
	assert.Equal(t, "heuristic", e.Meta["temporal_env_source"],
		"heuristic helper stays heuristic even with a const default")
}

func TestEnvFallbackAllowlist_ConfigPromotesHelper(t *testing.T) {
	// A helper that is neither built-in NOR "env"-named is invisible to both
	// layers by default — but installing it in the per-repo corporate
	// allow-list promotes it to the allowlist tier (source=allowlist).
	src := `package wf

import (
	"go.temporal.io/sdk/workflow"
	"example.com/app/cfg"
)

func WF(ctx workflow.Context) {
	actName := cfg.FetchActivityName("CHARGE_ACTIVITY", "ChargeActivity")
	workflow.ExecuteActivity(ctx, actName, 1)
}
`
	// Default: the unknown helper is not recognised (the dispatch keeps the
	// bare variable name, no env_default flag).
	def := temporalEdgesByVia(runGoExtract(t, src), "temporal.stub")
	require.Len(t, def, 1)
	assert.Equal(t, "actName", def[0].Meta["temporal_name"])
	_, flagged := def[0].Meta["temporal_name_origin"]
	assert.False(t, flagged, "unknown non-env helper must not be flagged by default")

	// With the corporate allow-list, the same helper resolves to its literal
	// default at the allowlist tier.
	got := temporalEdgesByVia(runGoExtractWithEnvHelpers(t, src, []string{"FetchActivityName"}), "temporal.stub")
	require.Len(t, got, 1)
	assert.Equal(t, "ChargeActivity", got[0].Meta["temporal_name"])
	assert.Equal(t, "env_default", got[0].Meta["temporal_name_origin"])
	assert.Equal(t, "allowlist", got[0].Meta["temporal_env_source"])
}

func TestEnvFallbackViaHelper_UnknownHelperNotFlagged(t *testing.T) {
	// A helper whose name is NOT in the tight allow-list must not be
	// treated as an env-default — precision over recall.
	fix := runGoExtract(t, `package wf

import (
	"go.temporal.io/sdk/workflow"
	"example.com/app/wfutils"
)

func WF(ctx workflow.Context) {
	actName := wfutils.PickActivity("CHARGE_ACTIVITY", "ChargeActivity")
	workflow.ExecuteActivity(ctx, actName, 1)
}
`)
	edges := temporalEdgesByVia(fix, "temporal.stub")
	require.Len(t, edges, 1)
	assert.Equal(t, "actName", edges[0].Meta["temporal_name"])
	_, flagged := edges[0].Meta["temporal_name_origin"]
	assert.False(t, flagged, "unknown helper must not be flagged env_default")
}

// --- G2: func-returning-literal dispatch ----------------------------
//
// When the activity-name argument is a call_expression (`pkg.GetName()`),
// the extractor cannot resolve it at parse time. It emits a stub with
// temporal_name_func=<callee> so the resolver can join it to the func's
// const-return literal. A parallel pass stamps temporal_const_return on
// single-return-literal functions.

// TestGoTemporal_FuncCallArg_EmitsNameFunc asserts that when a dispatch
// argument is a call_expression (`pkg.GetChargeName()`), the extractor
// emits a stub edge with temporal_name_func=<callee> (NOT temporal_name)
// so the resolver can join it to the func's const-return literal.
func TestGoTemporal_FuncCallArg_EmitsNameFunc(t *testing.T) {
	fix := runGoExtract(t, `package wf

import (
	"go.temporal.io/sdk/workflow"
	"example.com/names"
)

func WF(ctx workflow.Context) {
	workflow.ExecuteActivity(ctx, names.GetChargeName())
}
`)
	edges := temporalEdgesByVia(fix, "temporal.stub")
	require.Len(t, edges, 1)
	e := edges[0]
	assert.Equal(t, "GetChargeName", e.Meta["temporal_name_func"],
		"call_expression arg must emit the callee func name under temporal_name_func")
	assert.NotEmpty(t, e.To, "stub edge must have a placeholder To")
}

// TestGoTemporal_FuncCallArg_BareIdentifierCallee asserts that a bare func
// call (not a selector) is also captured under temporal_name_func.
func TestGoTemporal_FuncCallArg_BareIdentifierCallee(t *testing.T) {
	fix := runGoExtract(t, `package wf

import "go.temporal.io/sdk/workflow"

func WF(ctx workflow.Context) {
	workflow.ExecuteActivity(ctx, GetChargeName())
}
`)
	edges := temporalEdgesByVia(fix, "temporal.stub")
	require.Len(t, edges, 1)
	assert.Equal(t, "GetChargeName", edges[0].Meta["temporal_name_func"])
}

// TestGoTemporal_ConstReturnFunc_StampsTemporalConstReturn asserts that a
// function whose body is a single `return "<literal>"` gets
// temporal_const_return=<literal> stamped on its node, and that a function
// with branching returns does NOT receive that stamp.
func TestGoTemporal_ConstReturnFunc_StampsTemporalConstReturn(t *testing.T) {
	fix := runGoExtract(t, `package names

func GetChargeName() string {
	return "ChargeActivity"
}

func GetMultiName() string {
	if true {
		return "A"
	}
	return "B"
}
`)
	var charge, notConst *graph.Node
	for _, n := range fix.nodesByKind[graph.KindFunction] {
		if n.Name == "GetChargeName" {
			charge = n
		}
		if n.Name == "GetMultiName" {
			notConst = n
		}
	}
	require.NotNil(t, charge, "GetChargeName node must exist")
	require.NotNil(t, notConst, "GetMultiName node must exist")
	assert.Equal(t, "ChargeActivity", charge.Meta["temporal_const_return"],
		"single-return-literal func must be stamped temporal_const_return")
	_, flagged := notConst.Meta["temporal_const_return"]
	assert.False(t, flagged,
		"a function with branching returns must not be stamped temporal_const_return")
}

// TestGoTemporal_ConstReturnMethod_StampsTemporalConstReturn asserts that a
// method (not a top-level function) whose body is a single `return "<literal>"`
// also receives the temporal_const_return stamp.
func TestGoTemporal_ConstReturnMethod_StampsTemporalConstReturn(t *testing.T) {
	fix := runGoExtract(t, `package names

type Names struct{}

func (n Names) GetChargeName() string {
	return "ChargeActivity"
}
`)
	var method *graph.Node
	for _, n := range fix.nodesByKind[graph.KindMethod] {
		if n.Name == "GetChargeName" {
			method = n
		}
	}
	require.NotNil(t, method, "GetChargeName method node must exist")
	assert.Equal(t, "ChargeActivity", method.Meta["temporal_const_return"],
		"single-return-literal method must be stamped temporal_const_return")
}

// --- Outbound signal sends / query calls ----------------------------
//
// Consumer side of the signal/query namespaces: signalling or querying
// an already-running workflow by name (EdgeCalls tagged
// via=temporal.signal-send / temporal.query-call, name = 4th positional
// string literal).

func TestGoTemporal_SignalExternalWorkflow(t *testing.T) {
	fix := runGoExtract(t, `package wf

import "go.temporal.io/sdk/workflow"

func Orchestrator(ctx workflow.Context) error {
	return workflow.SignalExternalWorkflow(ctx, "order-123", "", "cancel-request", nil).Get(ctx, nil)
}
`)
	edges := temporalEdgesByVia(fix, "temporal.signal-send")
	require.Len(t, edges, 1)
	assert.Equal(t, "signal", edges[0].Meta["temporal_kind"])
	assert.Equal(t, "cancel-request", edges[0].Meta["temporal_name"])
}

func TestGoTemporal_ClientSignalWorkflow(t *testing.T) {
	fix := runGoExtract(t, `package svc

func Cancel(c Client) error {
	return c.SignalWorkflow(ctx, "order-123", "", "cancel-request", nil)
}
`)
	edges := temporalEdgesByVia(fix, "temporal.signal-send")
	require.Len(t, edges, 1)
	assert.Equal(t, "signal", edges[0].Meta["temporal_kind"])
	assert.Equal(t, "cancel-request", edges[0].Meta["temporal_name"])
}

func TestGoTemporal_ClientQueryWorkflow(t *testing.T) {
	fix := runGoExtract(t, `package svc

func Status(c Client) {
	c.QueryWorkflow(ctx, "order-123", "", "get-status")
}
`)
	edges := temporalEdgesByVia(fix, "temporal.query-call")
	require.Len(t, edges, 1)
	assert.Equal(t, "query", edges[0].Meta["temporal_kind"])
	assert.Equal(t, "get-status", edges[0].Meta["temporal_name"])
}

func TestGoTemporal_OutboundNonLiteralNameUndetected(t *testing.T) {
	fix := runGoExtract(t, `package svc

func Cancel(c Client, name string) error {
	return c.SignalWorkflow(ctx, "order-123", "", name, nil)
}
`)
	assert.Empty(t, temporalEdgesByVia(fix, "temporal.signal-send"))
}

func TestGoTemporal_SignalExternalAliasedNotDetected(t *testing.T) {
	fix := runGoExtract(t, `package wf

import wf "go.temporal.io/sdk/workflow"

func Orchestrator(ctx wf.Context) error {
	return wf.SignalExternalWorkflow(ctx, "order-123", "", "cancel-request", nil).Get(ctx, nil)
}
`)
	assert.Empty(t, temporalEdgesByVia(fix, "temporal.signal-send"))
}

// --- Wrapper-following: parser plumbing -----------------------------

func TestGoTemporal_WrapperFunc_TaggedAndArgsRecorded(t *testing.T) {
	fix := runGoExtract(t, `package wf

import "go.temporal.io/sdk/workflow"

// executeActivity is a dispatch wrapper: its `+"`name`"+` parameter flows
// straight into workflow.ExecuteActivity.
func executeActivity(ctx workflow.Context, name string, args ...any) error {
	return workflow.ExecuteActivity(ctx, name, args).Get(ctx, nil)
}

func OrderWorkflow(ctx workflow.Context) error {
	return executeActivity(ctx, "ChargeCard", 1)
}
`)
	// The wrapper's internal dispatch stub is flagged temporal_name_param.
	var wrapperStub *graph.Edge
	for _, e := range fix.edgesByKind[graph.EdgeCalls] {
		if e.Meta != nil && e.Meta["via"] == "temporal.stub" && e.Meta["temporal_name_param"] != nil {
			wrapperStub = e
		}
	}
	require.NotNil(t, wrapperStub, "wrapper's internal dispatch must be flagged temporal_name_param")
	assert.Equal(t, "name", wrapperStub.Meta["temporal_name_param"])

	// The call site executeActivity(ctx, "ChargeCard", 1) records arg_names.
	var wrapperCall *graph.Edge
	for _, e := range fix.edgesByKind[graph.EdgeCalls] {
		if e.To == "unresolved::executeActivity" {
			wrapperCall = e
		}
	}
	require.NotNil(t, wrapperCall, "call to wrapper must exist")
	names, _ := wrapperCall.Meta["arg_names"].([]string)
	require.GreaterOrEqual(t, len(names), 2)
	assert.Equal(t, "ChargeCard", names[1], "arg_names must capture the literal at position 1")
}

// --- StartWorker family tests ---

func TestGoTemporal_StartWorker_Basic(t *testing.T) {
	// wi.StartWorker([]any{workflow.XXX, workflow.YYY}, []any{activity.AAA, activity.BBB})
	// must emit 4 temporal.register edges (2 workflow + 2 activity).
	fix := runGoExtract(t, `package main

import "example.com/workflows"
import "example.com/activities"

func run(wi WorkflowInfo) {
	wi.StartWorker(
		[]any{workflows.OrderWorkflow, workflows.ShipWorkflow},
		[]any{activities.ChargeCard, activities.ProcessPayment},
	)
}
`)
	edges := temporalEdgesByVia(fix, "temporal.register")
	require.Len(t, edges, 4)
	byKind := map[string][]string{}
	for _, e := range edges {
		kind, _ := e.Meta["temporal_kind"].(string)
		name, _ := e.Meta["temporal_name"].(string)
		byKind[kind] = append(byKind[kind], name)
	}
	assert.ElementsMatch(t, []string{"OrderWorkflow", "ShipWorkflow"}, byKind["workflow"])
	assert.ElementsMatch(t, []string{"ChargeCard", "ProcessPayment"}, byKind["activity"])
}

func TestGoTemporal_StartWorkerWithOptions(t *testing.T) {
	fix := runGoExtract(t, `package main

import "example.com/activities"

func run(wi WorkflowInfo) {
	wi.StartWorkerWithOptions(
		[]any{},
		[]any{activities.Validate},
		WorkerOptions{MaxConcurrent: 10},
	)
}
`)
	edges := temporalEdgesByVia(fix, "temporal.register")
	require.Len(t, edges, 1)
	assert.Equal(t, "activity", edges[0].Meta["temporal_kind"])
	assert.Equal(t, "Validate", edges[0].Meta["temporal_name"])
}

func TestGoTemporal_StartWorkerWithInterceptors(t *testing.T) {
	fix := runGoExtract(t, `package main

import "example.com/workflows"

func run(wi WorkflowInfo) {
	wi.StartWorkerWithInterceptors(
		[]any{workflows.MainWorkflow},
		[]any{},
		nil,
	)
}
`)
	edges := temporalEdgesByVia(fix, "temporal.register")
	require.Len(t, edges, 1)
	assert.Equal(t, "workflow", edges[0].Meta["temporal_kind"])
	assert.Equal(t, "MainWorkflow", edges[0].Meta["temporal_name"])
}

func TestGoTemporal_StartWorker_SkipsNonSymbolElements(t *testing.T) {
	// Call expressions inside []any{…} (e.g. activities.New(provider))
	// should be silently dropped — goTemporalNameFromExpr returns "" for them.
	fix := runGoExtract(t, `package main

import "example.com/activities"

func run(wi WorkflowInfo) {
	wi.StartWorker(
		[]any{},
		[]any{activities.Charge, activities.New(provider)},
	)
}
`)
	edges := temporalEdgesByVia(fix, "temporal.register")
	require.Len(t, edges, 1)
	assert.Equal(t, "Charge", edges[0].Meta["temporal_name"])
}

func TestGoTemporal_StartWorker_EmptySlices_NoEdges(t *testing.T) {
	fix := runGoExtract(t, `package main

func run(wi WorkflowInfo) {
	wi.StartWorker([]any{}, []any{})
}
`)
	edges := temporalEdgesByVia(fix, "temporal.register")
	assert.Empty(t, edges, "empty []any slices must emit no register edges")
}

func TestGoTemporal_StartWorker_ReceiverCall(t *testing.T) {
	// (&wi).StartWorker(...) — selector on a parenthesised receiver.
	fix := runGoExtract(t, `package main

import "example.com/activities"

func run(wi *WorkflowInfo) {
	(&wi).StartWorker(
		[]any{},
		[]any{activities.Send},
	)
}
`)
	edges := temporalEdgesByVia(fix, "temporal.register")
	require.Len(t, edges, 1)
	assert.Equal(t, "Send", edges[0].Meta["temporal_name"])
}

func TestGoTemporal_StartWorker_NotOnOtherMethod(t *testing.T) {
	// A method named StartWorker on a non-WorkflowInfo receiver should
	// still be detected (we match on method name only, like RegisterActivity).
	// This test documents the current design choice.
	fix := runGoExtract(t, `package main

import "example.com/workflows"

func run(s Something) {
	s.StartWorker(
		[]any{workflows.XWorkflow},
		[]any{},
	)
}
`)
	edges := temporalEdgesByVia(fix, "temporal.register")
	require.Len(t, edges, 1)
	assert.Equal(t, "XWorkflow", edges[0].Meta["temporal_name"])
}
