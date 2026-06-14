package indexer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
)

// newTestIndexerGoJava builds an indexer that parses both Go and Java,
// for the cross-language Temporal bridge tests.
func newTestIndexerGoJava(g graph.Store) *Indexer {
	reg := parser.NewRegistry()
	reg.Register(languages.NewGoExtractor())
	reg.Register(languages.NewJavaExtractor())
	cfg := config.Default().Index
	cfg.Workers = 2
	return New(g, reg, cfg, zap.NewNop())
}

// TestTemporalE2E_GoWorkflowToActivity exercises the full pipeline —
// parser detection → graph emission → resolver rewriting — on a tiny
// Go fixture that registers an activity + a workflow and dispatches
// the activity from the workflow body. After indexing, the
// EdgeCalls placeholder must point at the real activity function
// node and both the activity and the workflow must carry
// `temporal_role` Meta tags.
func TestTemporalE2E_GoWorkflowToActivity(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

func OrderWorkflow(ctx workflow.Context, id string) error {
	return workflow.ExecuteActivity(ctx, ChargeCard, id).Get(ctx, nil)
}
`)
	writeFile(t, filepath.Join(dir, "activity.go"), `package wf

import "context"

func ChargeCard(ctx context.Context, id string) error {
	return nil
}
`)
	writeFile(t, filepath.Join(dir, "main.go"), `package wf

func setupWorker(w Worker) {
	w.RegisterWorkflow(OrderWorkflow)
	w.RegisterActivity(ChargeCard)
}
`)

	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	// The activity function node was discovered via the
	// `worker.RegisterActivity` edge and stamped temporal_role.
	activityNodes := g.FindNodesByName("ChargeCard")
	require.Len(t, activityNodes, 1)
	activity := activityNodes[0]
	assert.Equal(t, "activity", activity.Meta["temporal_role"],
		"registered activity must carry temporal_role meta")
	assert.Equal(t, "ChargeCard", activity.Meta["temporal_name"])

	// The workflow was stamped too.
	workflowNodes := g.FindNodesByName("OrderWorkflow")
	require.Len(t, workflowNodes, 1)
	wf := workflowNodes[0]
	assert.Equal(t, "workflow", wf.Meta["temporal_role"])

	// The workflow.ExecuteActivity call edge was rewritten from the
	// placeholder to the real activity function.
	var stubCall *graph.Edge
	for _, e := range g.GetOutEdges(wf.ID) {
		if e == nil || e.Meta == nil {
			continue
		}
		if e.Meta["via"] == "temporal.stub" {
			stubCall = e
			break
		}
	}
	require.NotNil(t, stubCall, "workflow must have an outbound temporal.stub edge")
	assert.Equal(t, activity.ID, stubCall.To,
		"stub-call edge must land on the registered activity, not the placeholder")
	assert.Equal(t, graph.OriginASTResolved, stubCall.Origin)
}

// TestTemporalE2E_GoChildWorkflow exercises the same pipeline on a
// child-workflow dispatch — a different temporal_kind, same resolver
// path.
func TestTemporalE2E_GoChildWorkflow(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "parent.go"), `package wf

import "go.temporal.io/sdk/workflow"

func ParentWorkflow(ctx workflow.Context) error {
	return workflow.ExecuteChildWorkflow(ctx, ChildWorkflow, 42).Get(ctx, nil)
}
`)
	writeFile(t, filepath.Join(dir, "child.go"), `package wf

import "go.temporal.io/sdk/workflow"

func ChildWorkflow(ctx workflow.Context, n int) error {
	return nil
}
`)
	writeFile(t, filepath.Join(dir, "main.go"), `package wf

func setup(w Worker) {
	w.RegisterWorkflow(ParentWorkflow)
	w.RegisterWorkflow(ChildWorkflow)
}
`)

	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	parent := g.FindNodesByName("ParentWorkflow")[0]
	child := g.FindNodesByName("ChildWorkflow")[0]
	assert.Equal(t, "workflow", parent.Meta["temporal_role"])
	assert.Equal(t, "workflow", child.Meta["temporal_role"])

	var stubCall *graph.Edge
	for _, e := range g.GetOutEdges(parent.ID) {
		if e != nil && e.Meta != nil && e.Meta["via"] == "temporal.stub" {
			stubCall = e
			break
		}
	}
	require.NotNil(t, stubCall, "parent workflow must have an outbound temporal.stub edge")
	assert.Equal(t, child.ID, stubCall.To)
	assert.Equal(t, "workflow", stubCall.Meta["temporal_kind"])
}

// TestTemporalE2E_GoEnvDefaultActivity exercises the env-var-with-literal
// -default dispatch name: the workflow names its activity through a
// variable read from os.Getenv with a literal fallback. The pipeline must
// land the call on the default activity but at the speculative tier.
func TestTemporalE2E_GoEnvDefaultActivity(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "workflow.go"), `package wf

import (
	"cmp"
	"os"

	"go.temporal.io/sdk/workflow"
)

func OrderWorkflow(ctx workflow.Context, id string) error {
	actName := cmp.Or(os.Getenv("CHARGE_ACTIVITY"), "ChargeCard")
	return workflow.ExecuteActivity(ctx, actName, id).Get(ctx, nil)
}
`)
	writeFile(t, filepath.Join(dir, "activity.go"), `package wf

import "context"

func ChargeCard(ctx context.Context, id string) error {
	return nil
}
`)
	writeFile(t, filepath.Join(dir, "main.go"), `package wf

func setupWorker(w Worker) {
	w.RegisterWorkflow(OrderWorkflow)
	w.RegisterActivity(ChargeCard)
}
`)

	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	wf := g.FindNodesByName("OrderWorkflow")[0]
	activity := g.FindNodesByName("ChargeCard")[0]

	var stubCall *graph.Edge
	for _, e := range g.GetOutEdges(wf.ID) {
		if e != nil && e.Meta != nil && e.Meta["via"] == "temporal.stub" {
			stubCall = e
			break
		}
	}
	require.NotNil(t, stubCall, "workflow must have an outbound temporal.stub edge")
	assert.Equal(t, activity.ID, stubCall.To,
		"env-default dispatch must land on the default activity")
	assert.Equal(t, "env_default", stubCall.Meta["temporal_name_origin"])
	assert.Equal(t, "os_getenv", stubCall.Meta["temporal_env_source"])
	assert.Equal(t, graph.OriginASTInferred, stubCall.Origin,
		"provable os.Getenv env-default lands at the inferred (visible) tier")
	_, hidden := stubCall.Meta[graph.MetaSpeculative]
	assert.False(t, hidden, "os.Getenv env-default is visible by default")
}

// TestEnvFallbackResolution (G1) exercises the env-with-fallback-via-helper
// dispatch name: the workflow names its activity through a variable read
// from a project-local helper (`wfutils.GetEnvOrDefault(KEY, "Default")`)
// rather than os.Getenv directly. The helper body lives in another package
// and is invisible at extract time, so the literal 2nd argument is taken as
// the default and the call lands on the default activity at the speculative
// tier.
func TestEnvFallbackResolution(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "workflow.go"), `package wf

import (
	"go.temporal.io/sdk/workflow"
)

func GetEnvOrDefault(key, def string) string { return def }

func OrderWorkflow(ctx workflow.Context, id string) error {
	actName := GetEnvOrDefault("CHARGE_ACTIVITY", "ChargeCard")
	return workflow.ExecuteActivity(ctx, actName, id).Get(ctx, nil)
}
`)
	writeFile(t, filepath.Join(dir, "activity.go"), `package wf

import "context"

func ChargeCard(ctx context.Context, id string) error {
	return nil
}
`)
	writeFile(t, filepath.Join(dir, "main.go"), `package wf

func setupWorker(w Worker) {
	w.RegisterWorkflow(OrderWorkflow)
	w.RegisterActivity(ChargeCard)
}
`)

	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	wf := g.FindNodesByName("OrderWorkflow")[0]
	activity := g.FindNodesByName("ChargeCard")[0]

	var stubCall *graph.Edge
	for _, e := range g.GetOutEdges(wf.ID) {
		if e != nil && e.Meta != nil && e.Meta["via"] == "temporal.stub" {
			stubCall = e
			break
		}
	}
	require.NotNil(t, stubCall, "workflow must have an outbound temporal.stub edge")
	assert.Equal(t, activity.ID, stubCall.To,
		"helper env-default dispatch must land on the default activity")
	assert.Equal(t, "env_default", stubCall.Meta["temporal_name_origin"])
	assert.Equal(t, "allowlist", stubCall.Meta["temporal_env_source"])
	assert.Equal(t, graph.OriginASTInferred, stubCall.Origin,
		"allow-list helper env-default lands at the inferred (visible) tier")
	_, hidden := stubCall.Meta[graph.MetaSpeculative]
	assert.False(t, hidden, "allow-list env-default is visible by default")
}

// TestTemporalE2E_GoEnvHeuristicSpeculative exercises the generic recall
// layer end-to-end: a helper NOT in the allow-list but whose name contains
// "env" is recognised by the heuristic, still resolves to the default
// activity, but lands at the HIDDEN speculative tier (source=heuristic) — the
// LLM cleaning pass is what later confirms or prunes it.
func TestTemporalE2E_GoEnvHeuristicSpeculative(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

func ActivityFromEnv(key, def string) string { return def }

func OrderWorkflow(ctx workflow.Context, id string) error {
	actName := ActivityFromEnv("CHARGE_ACTIVITY", "ChargeCard")
	return workflow.ExecuteActivity(ctx, actName, id).Get(ctx, nil)
}
`)
	writeFile(t, filepath.Join(dir, "activity.go"), `package wf

import "context"

func ChargeCard(ctx context.Context, id string) error { return nil }
`)
	writeFile(t, filepath.Join(dir, "main.go"), `package wf

func setupWorker(w Worker) {
	w.RegisterWorkflow(OrderWorkflow)
	w.RegisterActivity(ChargeCard)
}
`)

	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	wf := g.FindNodesByName("OrderWorkflow")[0]
	activity := g.FindNodesByName("ChargeCard")[0]

	var stubCall *graph.Edge
	for _, e := range g.GetOutEdges(wf.ID) {
		if e != nil && e.Meta != nil && e.Meta["via"] == "temporal.stub" {
			stubCall = e
			break
		}
	}
	require.NotNil(t, stubCall, "workflow must have an outbound temporal.stub edge")
	assert.Equal(t, activity.ID, stubCall.To,
		"heuristic env-default still resolves to the default activity")
	assert.Equal(t, "heuristic", stubCall.Meta["temporal_env_source"])
	assert.Equal(t, graph.OriginSpeculative, stubCall.Origin,
		"heuristic env-default stays at the speculative tier")
	assert.Equal(t, true, stubCall.Meta[graph.MetaSpeculative],
		"heuristic env-default is hidden by default")
}

// TestTemporalE2E_JavaInvokerToGoBridge exercises the Java invoker detector: a
// Java class dispatches a Go workflow through a configured custom invoker
// (`invoker.invokeAsync("ProcessOrderWorkflow", …)`), both as a string literal
// (exact, register-tier) and through an env property with a literal default
// (heuristic, hidden tier). Both must emit via=temporal.stub edges the resolver
// lands on the registered Go workflow.
func TestTemporalE2E_JavaInvokerToGoBridge(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "OrderManager.java"), `package com.example;

import io.temporal.workflow.WorkflowOptions;

public class OrderManager {
    private final Invoker invoker;

    public String startOrder(Object input) {
        WorkflowOptions options = WorkflowOptions.newBuilder()
            .setTaskQueue("order-wf").build();
        return invoker.invokeAsync("ProcessOrderWorkflow", options, input).block();
    }

    public String startWithDefault(Object input) {
        return invoker.invokeAsync(
            env.getProperty("order.workflow.type", "ProcessOrderWorkflow"),
            options, input).block();
    }
}
`)
	writeFile(t, filepath.Join(dir, "workflow.go"), `package main

import "go.temporal.io/sdk/workflow"

func ProcessOrderWorkflow(ctx workflow.Context, input string) error { return nil }
`)
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func setup(w Worker) { w.RegisterWorkflow(ProcessOrderWorkflow) }
`)

	g := graph.New()
	reg := parser.NewRegistry()
	reg.Register(languages.NewGoExtractor())
	reg.Register(languages.NewJavaExtractor())
	languages.ConfigureTemporalJavaInvokers(reg, []string{"Invoker"}, nil)
	cfg := config.Default().Index
	cfg.Workers = 2
	idx := New(g, reg, cfg, zap.NewNop())

	_, err := idx.Index(dir)
	require.NoError(t, err)

	goWf := g.FindNodesByName("ProcessOrderWorkflow")[0]

	javaStub := func(method string) *graph.Edge {
		nodes := g.FindNodesByName(method)
		require.NotEmpty(t, nodes, "java method %s must be a node", method)
		for _, e := range g.GetOutEdges(nodes[0].ID) {
			if e != nil && e.Meta != nil && e.Meta["via"] == "temporal.stub" {
				return e
			}
		}
		return nil
	}

	// Literal name → exact tier, resolves to the Go workflow, visible.
	exact := javaStub("startOrder")
	require.NotNil(t, exact, "startOrder must emit a temporal.stub")
	assert.Equal(t, "ProcessOrderWorkflow", exact.Meta["temporal_name"])
	assert.Equal(t, "workflow", exact.Meta["temporal_kind"])
	assert.Equal(t, true, exact.Meta["cross_language"])
	assert.Equal(t, goWf.ID, exact.To, "literal invoker dispatch resolves to the Go workflow")

	// Env-property default → heuristic tier, carries the env key, also resolves.
	heur := javaStub("startWithDefault")
	require.NotNil(t, heur, "startWithDefault must emit a temporal.stub")
	assert.Equal(t, "ProcessOrderWorkflow", heur.Meta["temporal_name"])
	assert.Equal(t, "heuristic", heur.Meta["temporal_env_source"])
	assert.Equal(t, "order.workflow.type", heur.Meta["temporal_env_key"])
	assert.Equal(t, goWf.ID, heur.To, "env-default invoker dispatch resolves to the Go workflow")
}

// TestTemporalE2E_JavaInvokerValueField exercises Java invoker priority 3: the
// dispatch name is a Spring `@Value("${key:Default}")`-injected field. The
// detector resolves the field to its SpEL literal default and lands the edge on
// the Go workflow at the heuristic tier.
func TestTemporalE2E_JavaInvokerValueField(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "OrderManager.java"), `package com.example;

import org.springframework.beans.factory.annotation.Value;

public class OrderManager {
    private final Invoker invoker;

    @Value("${order.workflow.type:ProcessOrderWorkflow}")
    private String workflowType;

    public String startConfigured(Object input) {
        return invoker.invokeAsync(workflowType, options, input).block();
    }
}
`)
	writeFile(t, filepath.Join(dir, "workflow.go"), `package main

import "go.temporal.io/sdk/workflow"

func ProcessOrderWorkflow(ctx workflow.Context, input string) error { return nil }
`)
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func setup(w Worker) { w.RegisterWorkflow(ProcessOrderWorkflow) }
`)

	g := graph.New()
	reg := parser.NewRegistry()
	reg.Register(languages.NewGoExtractor())
	reg.Register(languages.NewJavaExtractor())
	languages.ConfigureTemporalJavaInvokers(reg, []string{"Invoker"}, nil)
	cfg := config.Default().Index
	cfg.Workers = 2
	idx := New(g, reg, cfg, zap.NewNop())

	_, err := idx.Index(dir)
	require.NoError(t, err)

	goWf := g.FindNodesByName("ProcessOrderWorkflow")[0]
	nodes := g.FindNodesByName("startConfigured")
	require.NotEmpty(t, nodes)
	var stub *graph.Edge
	for _, e := range g.GetOutEdges(nodes[0].ID) {
		if e != nil && e.Meta != nil && e.Meta["via"] == "temporal.stub" {
			stub = e
			break
		}
	}
	require.NotNil(t, stub, "@Value-field invoker dispatch must emit a temporal.stub")
	assert.Equal(t, "ProcessOrderWorkflow", stub.Meta["temporal_name"])
	assert.Equal(t, "heuristic", stub.Meta["temporal_env_source"])
	assert.Equal(t, goWf.ID, stub.To, "@Value default resolves to the Go workflow")
}

// TestTemporalE2E_JavaInvokerOffWhenUnconfigured asserts zero behavioural
// change when java_temporal_invokers is not configured: the invoker call is
// left to the generic path, emitting no temporal.stub.
func TestTemporalE2E_JavaInvokerOffWhenUnconfigured(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "OrderManager.java"), `package com.example;

public class OrderManager {
    private final Invoker invoker;

    public String startOrder(Object input) {
        return invoker.invokeAsync("ProcessOrderWorkflow", input).block();
    }
}
`)
	g := graph.New()
	idx := newTestIndexerGoJava(g) // NOT configured with any invoker
	_, err := idx.Index(dir)
	require.NoError(t, err)

	for _, n := range g.FindNodesByName("startOrder") {
		for _, e := range g.GetOutEdges(n.ID) {
			if e != nil && e.Meta != nil && e.Meta["via"] == "temporal.stub" {
				t.Fatal("invoker detection must be OFF when java_temporal_invokers is unconfigured")
			}
		}
	}
}

// TestTemporalE2E_GoEnvConstDefault exercises the #1 corpus gap: an env-helper
// whose DEFAULT argument is a constant reference, not a string literal
// (`wfutil.GetEnvOrDefault(config.ACTIVITY_NAME_ENV, config.ACTIVITY_NAME_DEFAULT)`).
// The parser records temporal_default_const=ACTIVITY_NAME_DEFAULT (keeping
// temporal_name as the dispatch variable); the resolver substitutes the
// constant's literal value (ChargeActivity) through constVal and lands the edge
// on the registered activity at the const_ref tier (inferred, visible).
func TestTemporalE2E_GoEnvConstDefault(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/app\n\ngo 1.22\n")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "config"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "wfutil"), 0o755))

	writeFile(t, filepath.Join(dir, "config", "env.go"), `package config

const (
	ACTIVITY_NAME_ENV     = "CHARGE_ACTIVITY_NAME"
	ACTIVITY_NAME_DEFAULT = "ChargeActivity"
)
`)
	writeFile(t, filepath.Join(dir, "wfutil", "wfutil.go"), `package wfutil

func GetEnvOrDefault(key, def string) string { return def }
`)
	writeFile(t, filepath.Join(dir, "workflow.go"), `package app

import (
	"go.temporal.io/sdk/workflow"
	"example.com/app/config"
	"example.com/app/wfutil"
)

func OrderWorkflow(ctx workflow.Context, input string) error {
	name := wfutil.GetEnvOrDefault(config.ACTIVITY_NAME_ENV, config.ACTIVITY_NAME_DEFAULT)
	return workflow.ExecuteActivity(ctx, name, input).Get(ctx, nil)
}
`)
	writeFile(t, filepath.Join(dir, "activity.go"), `package app

import "context"

func ChargeActivity(ctx context.Context, input string) error { return nil }
`)
	writeFile(t, filepath.Join(dir, "main.go"), `package app

func setupWorker(w Worker) {
	w.RegisterWorkflow(OrderWorkflow)
	w.RegisterActivity(ChargeActivity)
}
`)

	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	wf := g.FindNodesByName("OrderWorkflow")[0]
	activity := g.FindNodesByName("ChargeActivity")[0]

	var stub *graph.Edge
	for _, e := range g.GetOutEdges(wf.ID) {
		if e != nil && e.Meta != nil && e.Meta["via"] == "temporal.stub" {
			stub = e
			break
		}
	}
	require.NotNil(t, stub, "workflow must have an outbound temporal.stub edge")
	assert.Equal(t, activity.ID, stub.To, "const-default dispatch must resolve to the activity")
	assert.Equal(t, "const_ref", stub.Meta["temporal_env_source"])
	assert.Equal(t, "ACTIVITY_NAME_DEFAULT", stub.Meta["temporal_default_const"])
	assert.Equal(t, "ChargeActivity", stub.Meta["temporal_const_value"])
	assert.Equal(t, graph.OriginASTInferred, stub.Origin, "const_ref lands at the inferred (visible) tier")
	_, hidden := stub.Meta[graph.MetaSpeculative]
	assert.False(t, hidden, "const_ref env-default is visible by default")
}

// TestTemporalE2E_GoQueryHandler exercises in-workflow handler detection:
// a workflow.SetQueryHandler call must surface as a via=temporal.handler
// edge from the enclosing workflow carrying its kind + name.
func TestTemporalE2E_GoQueryHandler(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

func StatusWorkflow(ctx workflow.Context) error {
	workflow.SetQueryHandler(ctx, "status", func() (string, error) { return "ok", nil })
	return nil
}
`)

	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	wf := g.FindNodesByName("StatusWorkflow")[0]
	var handler *graph.Edge
	for _, e := range g.GetOutEdges(wf.ID) {
		if e != nil && e.Meta != nil && e.Meta["via"] == "temporal.handler" {
			handler = e
			break
		}
	}
	require.NotNil(t, handler, "workflow must have an outbound temporal.handler edge")
	assert.Equal(t, "query", handler.Meta["temporal_kind"])
	assert.Equal(t, "status", handler.Meta["temporal_name"])
}

// TestTemporalE2E_GoConstNamedActivity exercises a string-const dispatch
// name: the workflow names its activity through a const whose literal
// value matches the registered activity. The resolver must substitute
// the const value and land the call on the real activity function.
func TestTemporalE2E_GoConstNamedActivity(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "constants.go"), `package wf

const ChargeActivity = "ChargeCard"
`)
	writeFile(t, filepath.Join(dir, "workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

func OrderWorkflow(ctx workflow.Context, id string) error {
	return workflow.ExecuteActivity(ctx, ChargeActivity, id).Get(ctx, nil)
}
`)
	writeFile(t, filepath.Join(dir, "activity.go"), `package wf

import "context"

func ChargeCard(ctx context.Context, id string) error {
	return nil
}
`)
	writeFile(t, filepath.Join(dir, "main.go"), `package wf

func setupWorker(w Worker) {
	w.RegisterWorkflow(OrderWorkflow)
	w.RegisterActivity(ChargeCard)
}
`)

	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	wf := g.FindNodesByName("OrderWorkflow")[0]
	activity := g.FindNodesByName("ChargeCard")[0]

	var stubCall *graph.Edge
	for _, e := range g.GetOutEdges(wf.ID) {
		if e != nil && e.Meta != nil && e.Meta["via"] == "temporal.stub" {
			stubCall = e
			break
		}
	}
	require.NotNil(t, stubCall, "workflow must have an outbound temporal.stub edge")
	assert.Equal(t, activity.ID, stubCall.To,
		"const-named dispatch must resolve to the activity via the const value")
	assert.Equal(t, "ChargeCard", stubCall.Meta["temporal_const_value"])
}

// TestTemporalE2E_GoWrapperFollowing exercises dispatch through a user
// wrapper: a workflow calls a helper `executeActivity(ctx, name, …)` that
// internally does workflow.ExecuteActivity(ctx, name, …). The pipeline
// must follow the wrapper and land the workflow on the real activity,
// for both a string-literal and a string-const argument.
func TestTemporalE2E_GoWrapperFollowing(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "wrapper.go"), `package wf

import "go.temporal.io/sdk/workflow"

func executeActivity(ctx workflow.Context, name string, args ...any) error {
	return workflow.ExecuteActivity(ctx, name, args).Get(ctx, nil)
}
`)
	writeFile(t, filepath.Join(dir, "constants.go"), `package wf

const RefundActivity = "RefundCard"
`)
	writeFile(t, filepath.Join(dir, "workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

func OrderWorkflow(ctx workflow.Context) error {
	if err := executeActivity(ctx, "ChargeCard", 1); err != nil {
		return err
	}
	return executeActivity(ctx, RefundActivity, 2)
}
`)
	writeFile(t, filepath.Join(dir, "activity.go"), `package wf

import "context"

func ChargeCard(ctx context.Context, n int) error { return nil }
func RefundCard(ctx context.Context, n int) error { return nil }
`)
	writeFile(t, filepath.Join(dir, "main.go"), `package wf

func setupWorker(w Worker) {
	w.RegisterWorkflow(OrderWorkflow)
	w.RegisterActivity(ChargeCard)
	w.RegisterActivity(RefundCard)
}
`)

	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	wf := g.FindNodesByName("OrderWorkflow")[0]
	charge := g.FindNodesByName("ChargeCard")[0]
	refund := g.FindNodesByName("RefundCard")[0]

	// Collect the workflow's outbound resolved temporal.stub targets.
	targets := map[string]bool{}
	for _, e := range g.GetOutEdges(wf.ID) {
		if e != nil && e.Meta != nil && e.Meta["via"] == "temporal.stub" && e.Meta["temporal_via_wrapper"] != nil {
			targets[e.To] = true
		}
	}
	assert.True(t, targets[charge.ID], "literal-arg wrapper dispatch must reach ChargeCard")
	assert.True(t, targets[refund.ID], "const-arg wrapper dispatch must reach RefundCard")
}

// TestTemporalE2E_GoWrapperDepth2 exercises depth>1 wrapper-following: a
// workflow passes a literal activity name into runStep, which forwards it to
// execActivity, which finally calls workflow.ExecuteActivity. The iterative
// wrapper pass must propagate the literal up the two-hop chain and land the
// top workflow on the registered activity.
func TestTemporalE2E_GoWrapperDepth2(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "wrappers.go"), `package wf

import "go.temporal.io/sdk/workflow"

func runStep(ctx workflow.Context, name string, args ...any) error {
	return execActivity(ctx, name, args)
}

func execActivity(ctx workflow.Context, name string, args ...any) error {
	return workflow.ExecuteActivity(ctx, name, args).Get(ctx, nil)
}
`)
	writeFile(t, filepath.Join(dir, "workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

func CancelWorkflow(ctx workflow.Context) error {
	return runStep(ctx, "ProcessCancel", 1)
}
`)
	writeFile(t, filepath.Join(dir, "activity.go"), `package wf

import "context"

func ProcessCancel(ctx context.Context, n int) error { return nil }
`)
	writeFile(t, filepath.Join(dir, "main.go"), `package wf

func setupWorker(w Worker) {
	w.RegisterWorkflow(CancelWorkflow)
	w.RegisterActivity(ProcessCancel)
}
`)

	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	wf := g.FindNodesByName("CancelWorkflow")[0]
	activity := g.FindNodesByName("ProcessCancel")[0]

	targets := map[string]bool{}
	for _, e := range g.GetOutEdges(wf.ID) {
		if e != nil && e.Meta != nil && e.Meta["via"] == "temporal.stub" && e.Meta["temporal_via_wrapper"] != nil {
			targets[e.To] = true
		}
	}
	assert.True(t, targets[activity.ID],
		"depth-2 wrapper dispatch must follow runStep->execActivity and reach ProcessCancel")
}

// TestTemporalE2E_GoSignalQueryLink links a Go signal sender / query
// caller to the workflow that handles them, by shared name.
func TestTemporalE2E_GoSignalQueryLink(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "order_workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

func OrderWorkflow(ctx workflow.Context) error {
	cancelCh := workflow.GetSignalChannel(ctx, "cancel-order")
	_ = cancelCh
	workflow.SetQueryHandler(ctx, "order-status", func() (string, error) { return "ok", nil })
	return nil
}
`)
	writeFile(t, filepath.Join(dir, "orchestrator.go"), `package wf

import "go.temporal.io/sdk/workflow"

func Orchestrator(ctx workflow.Context) error {
	return workflow.SignalExternalWorkflow(ctx, "order-123", "", "cancel-order", nil).Get(ctx, nil)
}
`)
	writeFile(t, filepath.Join(dir, "service.go"), `package wf

func CheckStatus(c Client) {
	c.QueryWorkflow(ctx, "order-123", "", "order-status")
}
`)

	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	order := g.FindNodesByName("OrderWorkflow")[0]
	orch := g.FindNodesByName("Orchestrator")[0]
	check := g.FindNodesByName("CheckStatus")[0]

	hasLink := func(fromID, via string) bool {
		for _, e := range g.GetOutEdges(fromID) {
			if e != nil && e.Meta != nil && e.Meta["via"] == via && e.To == order.ID {
				return true
			}
		}
		return false
	}
	assert.True(t, hasLink(orch.ID, "temporal.signal-link"),
		"Orchestrator must link to OrderWorkflow via signal cancel-order")
	assert.True(t, hasLink(check.ID, "temporal.query-link"),
		"CheckStatus must link to OrderWorkflow via query order-status")
}

// TestTemporalE2E_GoWfutilCrossPackage exercises Pattern 4/7: a workflow
// dispatches activities through a shared `workflowutils` package
// (ExecuteActivityMethod / ExecuteLocalActivity wrappers) AND directly.
// All forms must land on the registered activities.
func TestTemporalE2E_GoWfutilCrossPackage(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/app\n\ngo 1.22\n")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "wfutil"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "orders"), 0o755))

	writeFile(t, filepath.Join(dir, "wfutil", "wfutil.go"), `package wfutil

import "go.temporal.io/sdk/workflow"

func ExecuteActivityMethod(ctx workflow.Context, ao any, name string, args ...any) error {
	return workflow.ExecuteActivity(ctx, name, args).Get(ctx, nil)
}

func ExecuteLocalActivity(ctx workflow.Context, name string, args ...any) error {
	return workflow.ExecuteLocalActivity(ctx, name, args).Get(ctx, nil)
}
`)
	writeFile(t, filepath.Join(dir, "orders", "workflow.go"), `package orders

import (
	"go.temporal.io/sdk/workflow"

	"example.com/app/wfutil"
)

func OrderWorkflow(ctx workflow.Context) error {
	if err := wfutil.ExecuteActivityMethod(ctx, nil, "ChargeCard", 1); err != nil {
		return err
	}
	return wfutil.ExecuteLocalActivity(ctx, "AuditOrder", 2)
}
`)
	writeFile(t, filepath.Join(dir, "orders", "activity.go"), `package orders

import "context"

func ChargeCard(ctx context.Context, n int) error { return nil }
func AuditOrder(ctx context.Context, n int) error { return nil }
`)
	writeFile(t, filepath.Join(dir, "orders", "main.go"), `package orders

func setupWorker(w Worker) {
	w.RegisterWorkflow(OrderWorkflow)
	w.RegisterActivity(ChargeCard)
	w.RegisterActivity(AuditOrder)
}
`)

	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	wf := g.FindNodesByName("OrderWorkflow")[0]
	charge := g.FindNodesByName("ChargeCard")[0]
	audit := g.FindNodesByName("AuditOrder")[0]

	targets := map[string]bool{}
	for _, e := range g.GetOutEdges(wf.ID) {
		if e != nil && e.Meta != nil && e.Meta["via"] == "temporal.stub" {
			targets[e.To] = true
		}
	}
	assert.True(t, targets[charge.ID], "ExecuteActivityMethod wrapper must reach ChargeCard")
	assert.True(t, targets[audit.ID], "ExecuteLocalActivity wrapper must reach AuditOrder")
}

// TestTemporalE2E_GoStepExecutor exercises the step/executor pattern: a
// struct dispatches an activity via one of its fields, and is constructed
// with that field set to a string. The constructing function must reach
// the named activity.
func TestTemporalE2E_GoStepExecutor(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "executor.go"), `package wf

import "go.temporal.io/sdk/workflow"

type ActivityExecutor struct {
	ActivityName string
}

func (e *ActivityExecutor) Execute(ctx workflow.Context) error {
	return workflow.ExecuteActivity(ctx, e.ActivityName).Get(ctx, nil)
}
`)
	writeFile(t, filepath.Join(dir, "workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

func OrderWorkflow(ctx workflow.Context) error {
	step := &ActivityExecutor{ActivityName: "ChargeCard"}
	return step.Execute(ctx)
}
`)
	writeFile(t, filepath.Join(dir, "activity.go"), `package wf

import "context"

func ChargeCard(ctx context.Context) error { return nil }
`)
	writeFile(t, filepath.Join(dir, "main.go"), `package wf

func setupWorker(w Worker) {
	w.RegisterWorkflow(OrderWorkflow)
	w.RegisterActivity(ChargeCard)
}
`)

	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	wf := g.FindNodesByName("OrderWorkflow")[0]
	charge := g.FindNodesByName("ChargeCard")[0]

	found := false
	for _, e := range g.GetOutEdges(wf.ID) {
		if e != nil && e.Meta != nil && e.Meta["temporal_via_executor"] == true && e.To == charge.ID {
			found = true
		}
	}
	assert.True(t, found, "constructing OrderWorkflow must reach ChargeCard via the executor field")
}

// TestTemporalE2E_GoUnregisteredActivityByConvention exercises Pattern 2 /
// Stage 1.2: the activity function lives here but is registered by a
// separate worker-runner (no RegisterActivity in this workspace). The
// dispatch names it through a two-part const (ActivityFuncName), and the
// resolver must fall back to the function-name convention.
func TestTemporalE2E_GoUnregisteredActivityByConvention(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "activity.go"), `package wf

import "context"

// Registered elsewhere (a separate worker-runner); no RegisterActivity here.
func GetProductOfferingActivity(ctx context.Context) error { return nil }
`)
	writeFile(t, filepath.Join(dir, "constants.go"), `package wf

const (
	ActivityPackageName = "browse-product-catalog-activities"
	ActivityFuncName    = "GetProductOfferingActivity"
)
`)
	writeFile(t, filepath.Join(dir, "workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

func BrowseWorkflow(ctx workflow.Context) error {
	return workflow.ExecuteActivity(ctx, ActivityFuncName).Get(ctx, nil)
}
`)
	writeFile(t, filepath.Join(dir, "main.go"), `package wf

func setupWorker(w Worker) {
	w.RegisterWorkflow(BrowseWorkflow)
}
`)

	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	wf := g.FindNodesByName("BrowseWorkflow")[0]
	act := g.FindNodesByName("GetProductOfferingActivity")[0]

	var stub *graph.Edge
	for _, e := range g.GetOutEdges(wf.ID) {
		if e != nil && e.Meta != nil && e.Meta["via"] == "temporal.stub" {
			stub = e
		}
	}
	require.NotNil(t, stub)
	assert.Equal(t, act.ID, stub.To, "unregistered activity must resolve by func-name convention")
	assert.Equal(t, "convention", stub.Meta["temporal_resolution_via"])
	assert.Equal(t, graph.OriginASTInferred, stub.Origin, "convention match is inferred-tier")
}

// TestTemporalE2E_JavaToGoBridge links a Java @WorkflowInterface (start +
// signal + query) to the Go workflow that implements them, by canonical
// name — the cross-language bridge.
func TestTemporalE2E_JavaToGoBridge(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "order_workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

func OrderWorkflow(ctx workflow.Context, id string) error {
	workflow.GetSignalChannel(ctx, "cancel-order")
	workflow.SetQueryHandler(ctx, "order-status", func() (string, error) { return "ok", nil })
	return nil
}
`)
	writeFile(t, filepath.Join(dir, "main.go"), `package wf

func setup(w Worker) { w.RegisterWorkflow(OrderWorkflow) }
`)
	writeFile(t, filepath.Join(dir, "OrderWorkflowApi.java"), `package com.example.orders;

import io.temporal.workflow.WorkflowInterface;
import io.temporal.workflow.WorkflowMethod;
import io.temporal.workflow.SignalMethod;
import io.temporal.workflow.QueryMethod;

@WorkflowInterface
public interface OrderWorkflowApi {
    @WorkflowMethod(name = "OrderWorkflow")
    String process(String orderId);

    @SignalMethod(name = "cancel-order")
    void cancel(String reason);

    @QueryMethod(name = "order-status")
    String getStatus();
}
`)

	g := graph.New()
	idx := newTestIndexerGoJava(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)

	goWf := g.FindNodesByName("OrderWorkflow")[0]

	// Find the Java method nodes by name.
	javaMethod := func(name string) *graph.Node {
		for _, n := range g.FindNodesByName(name) {
			if n.Language == "java" {
				return n
			}
		}
		return nil
	}
	process := javaMethod("process")
	cancel := javaMethod("cancel")
	getStatus := javaMethod("getStatus")
	require.NotNil(t, process)
	require.NotNil(t, cancel)
	require.NotNil(t, getStatus)

	hasCrossLink := func(fromID, via string) bool {
		for _, e := range g.GetOutEdges(fromID) {
			if e != nil && e.Meta != nil && e.Meta["via"] == via &&
				e.Meta["cross_language"] == true && e.To == goWf.ID {
				return true
			}
		}
		return false
	}
	assert.True(t, hasCrossLink(process.ID, "temporal.start-workflow"),
		"Java @WorkflowMethod(name=OrderWorkflow) must link to the Go OrderWorkflow")
	assert.True(t, hasCrossLink(cancel.ID, "temporal.signal-link"),
		"Java @SignalMethod(cancel-order) must link to the Go workflow handling it")
	assert.True(t, hasCrossLink(getStatus.ID, "temporal.query-link"),
		"Java @QueryMethod(order-status) must link to the Go workflow handling it")
}

// TestFuncConstReturnDispatch_E2E exercises G2 (func-returning-literal
// dispatch): a workflow calls `names.GetChargeName()` as the activity-name
// argument. GetChargeName returns the string literal "ChargeActivity", and
// the worker registers an activity named "ChargeActivity". After indexing
// the full pipeline must resolve the stub edge to the registered activity.
func TestFuncConstReturnDispatch_E2E(t *testing.T) {
	dir := t.TempDir()

	// names/names.go — a helper package that returns the activity name.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "names"), 0o755))
	writeFile(t, filepath.Join(dir, "names", "names.go"), `package names

// GetChargeName returns the canonical activity name used by the workflow.
func GetChargeName() string {
	return "ChargeActivity"
}
`)

	// workflow.go — dispatches via names.GetChargeName().
	writeFile(t, filepath.Join(dir, "workflow.go"), `package wf

import (
	"go.temporal.io/sdk/workflow"
)

func names_GetChargeName() string { return "ChargeActivity" } // stub for same-package resolution

func OrderWorkflow(ctx workflow.Context, id string) error {
	return workflow.ExecuteActivity(ctx, names_GetChargeName()).Get(ctx, nil)
}
`)

	// activity.go — the registered activity function.
	writeFile(t, filepath.Join(dir, "activity.go"), `package wf

import "context"

func ChargeActivity(ctx context.Context, id string) error {
	return nil
}
`)

	// main.go — registers the workflow and the activity.
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

	wf := g.FindNodesByName("OrderWorkflow")[0]
	activity := g.FindNodesByName("ChargeActivity")[0]
	assert.Equal(t, "activity", activity.Meta["temporal_role"],
		"registered activity must carry temporal_role meta")

	var stubCall *graph.Edge
	for _, e := range g.GetOutEdges(wf.ID) {
		if e != nil && e.Meta != nil && e.Meta["via"] == "temporal.stub" {
			stubCall = e
			break
		}
	}
	require.NotNil(t, stubCall, "workflow must have an outbound temporal.stub edge")
	assert.Equal(t, activity.ID, stubCall.To,
		"func-const-return dispatch must resolve stub to the registered activity")
}
