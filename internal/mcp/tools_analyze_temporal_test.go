package mcp

import (
	"context"
	"encoding/json"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/zzet/gortex/internal/graph"
)

func TestAnalyzeTemporalOrphans(t *testing.T) {
	srv, _ := setupTestServer(t)
	// Broken dispatch: a stub that never resolved.
	srv.graph.AddEdge(&graph.Edge{
		From: "wf.go::OrderWorkflow", To: "unresolved::temporal::activity::MissingActivity",
		Kind: graph.EdgeCalls, FilePath: "wf.go", Line: 5,
		Meta: map[string]any{"via": "temporal.stub", "temporal_kind": "activity", "temporal_name": "MissingActivity"},
	})
	// Signal sent with no handler anywhere.
	srv.graph.AddEdge(&graph.Edge{
		From: "wf.go::OrderWorkflow", To: "unresolved::extern::workflow::SignalExternalWorkflow",
		Kind: graph.EdgeCalls, FilePath: "wf.go", Line: 8,
		Meta: map[string]any{"via": "temporal.signal-send", "temporal_kind": "signal", "temporal_name": "ghost-signal"},
	})

	req := mcplib.CallToolRequest{}
	req.Params.Name = "analyze"
	req.Params.Arguments = map[string]any{"kind": "temporal_orphans"}
	res, err := srv.handleAnalyze(context.Background(), req)
	if err != nil {
		t.Fatalf("handleAnalyze: %v", err)
	}
	if res.IsError {
		t.Fatalf("error: %+v", res.Content)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &out); err != nil {
		t.Fatalf("json: %v", err)
	}
	totals, _ := out["totals"].(map[string]any)
	if totals["broken_dispatch"].(float64) < 1 {
		t.Errorf("expected a broken dispatch, got %v", totals["broken_dispatch"])
	}
	if totals["signal_no_handler"].(float64) < 1 {
		t.Errorf("expected a signal with no handler, got %v", totals["signal_no_handler"])
	}
}
