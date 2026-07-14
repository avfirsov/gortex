package mcp

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/runtimeactivity"
)

func TestRunAnalysisBalancesActivityAndReportsStageTelemetry(t *testing.T) {
	t.Setenv("GORTEX_DAEMON_MEMRELEASE", "false")
	core, logs := observer.New(zap.InfoLevel)
	s := &Server{graph: graph.New(), logger: zap.New(core)}
	baseline := runtimeactivity.Current().Active

	s.RunAnalysis()

	if got := runtimeactivity.Current().Active; got != baseline {
		t.Fatalf("active work after analysis = %d, want %d", got, baseline)
	}
	entries := logs.FilterMessage("mcp: analysis pass complete").All()
	if len(entries) != 1 {
		t.Fatalf("analysis telemetry logs = %d, want 1", len(entries))
	}
	fields := entries[0].ContextMap()
	for _, key := range []string{
		"snapshot", "leiden", "processes", "pagerank", "adjacency",
		"auto_concepts", "hits", "total",
		"heap_alloc_before_bytes", "heap_alloc_after_bytes",
		"heap_inuse_before_bytes", "heap_inuse_after_bytes",
		"heap_idle_before_bytes", "heap_idle_after_bytes",
		"heap_released_before_bytes", "heap_released_after_bytes",
		"heap_sys_before_bytes", "heap_sys_after_bytes",
		"stack_inuse_before_bytes", "stack_inuse_after_bytes",
	} {
		if _, ok := fields[key]; !ok {
			t.Errorf("analysis telemetry missing %q: %#v", key, fields)
		}
	}
}
