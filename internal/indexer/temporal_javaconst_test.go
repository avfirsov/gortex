package indexer

import (
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

// TestTemporalE2E_JavaInvokerConstRef exercises Java invoker priority 4:
// the dispatch name is a constant reference (`Constants.WORKFLOW_TYPE`).
// The Java extractor records temporal_name=WORKFLOW_TYPE,
// temporal_env_source=const_ref; the constant's literal value lives in a
// Java `public static final String` field. For the edge to resolve, that
// Java constant must reach the resolver's constVal index (Java string
// constants are KindField nodes, so the parser must stamp Meta["value"]
// and buildTemporalIndex must ingest KindField + the resolver derefs
// WORKFLOW_TYPE → "ProcessOrderWorkflow" → the registered Go workflow).
//
// KEYWORDS: temporal, java, const_ref, constVal, cross-language, P1
func TestTemporalE2E_JavaInvokerConstRef(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "Constants.java"), `package com.example;

public final class Constants {
    public static final String WORKFLOW_TYPE = "ProcessOrderWorkflow";
}
`)
	writeFile(t, filepath.Join(dir, "OrderManager.java"), `package com.example;

import io.temporal.workflow.WorkflowOptions;

public class OrderManager {
    private final Invoker invoker;

    public String startOrder(Object input) {
        WorkflowOptions options = WorkflowOptions.newBuilder()
            .setTaskQueue("order-wf").build();
        return invoker.invokeAsync(Constants.WORKFLOW_TYPE, options, input).block();
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

	goWf := g.FindNodesByName("ProcessOrderWorkflow")
	require.NotEmpty(t, goWf)

	nodes := g.FindNodesByName("startOrder")
	require.NotEmpty(t, nodes)
	var stub *graph.Edge
	for _, e := range g.GetOutEdges(nodes[0].ID) {
		if e != nil && e.Meta != nil && e.Meta["via"] == "temporal.stub" {
			stub = e
			break
		}
	}
	require.NotNil(t, stub, "const-ref invoker dispatch must emit a temporal.stub")
	assert.Equal(t, "WORKFLOW_TYPE", stub.Meta["temporal_name"])
	assert.Equal(t, "const_ref", stub.Meta["temporal_env_source"])
	assert.Equal(t, goWf[0].ID, stub.To,
		"Java const_ref dispatch must resolve via Java constVal to the Go workflow")
	assert.Equal(t, "ProcessOrderWorkflow", stub.Meta["temporal_const_value"])
}
