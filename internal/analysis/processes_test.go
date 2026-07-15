package analysis

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func processFixture(entryCount, leafCount int) ([]*graph.Node, []*graph.Edge) {
	nodes := make([]*graph.Node, 0, entryCount+leafCount)
	edges := make([]*graph.Edge, 0, entryCount*leafCount)
	for i := 0; i < leafCount; i++ {
		nodes = append(nodes, &graph.Node{
			ID:       fmt.Sprintf("leaf-%04d", i),
			Name:     fmt.Sprintf("parseLeaf%04d", i),
			Kind:     graph.KindFunction,
			Language: "go",
			FilePath: fmt.Sprintf("pkg/leaf_%04d.go", i),
		})
	}
	for i := 0; i < entryCount; i++ {
		id := fmt.Sprintf("entry-%04d", i)
		nodes = append(nodes, &graph.Node{
			ID:       id,
			Name:     fmt.Sprintf("Serve%04d", i),
			Kind:     graph.KindFunction,
			Language: "go",
			FilePath: fmt.Sprintf("cmd/entry_%04d.go", i),
		})
		for j := leafCount - 1; j >= 0; j-- {
			edges = append(edges, &graph.Edge{
				From: id,
				To:   fmt.Sprintf("leaf-%04d", j),
				Kind: graph.EdgeCalls,
			})
		}
	}
	return nodes, edges
}

func TestDiscoverProcessesKeepsSmallGraphComplete(t *testing.T) {
	nodes := []*graph.Node{
		{ID: "root", Name: "ServeRoot", Kind: graph.KindFunction, Language: "go", FilePath: "cmd/root.go"},
		{ID: "child", Name: "parseChild", Kind: graph.KindFunction, Language: "go", FilePath: "pkg/child.go"},
		{ID: "leaf", Name: "parseLeaf", Kind: graph.KindFunction, Language: "go", FilePath: "pkg/leaf.go"},
	}
	edges := []*graph.Edge{
		{From: "root", To: "child", Kind: graph.EdgeCalls},
		{From: "child", To: "leaf", Kind: graph.EdgeCalls},
	}

	got := discoverProcesses(nodes, edges, ProcessLimits{})
	if got.Truncated || got.TruncationReason != "" {
		t.Fatalf("small graph unexpectedly truncated: %#v", got)
	}
	if len(got.Processes) != 1 {
		t.Fatalf("processes = %d, want 1", len(got.Processes))
	}
	wantSteps := []Step{{ID: "root", Depth: 0}, {ID: "child", Depth: 1}, {ID: "leaf", Depth: 2}}
	if !reflect.DeepEqual(got.Processes[0].Steps, wantSteps) {
		t.Fatalf("steps = %#v, want %#v", got.Processes[0].Steps, wantSteps)
	}
	if got.Processes[0].Truncated {
		t.Fatal("complete process marked truncated")
	}
}

func TestDiscoverProcessesBoundsRetainedStepsDeterministically(t *testing.T) {
	nodes, edges := processFixture(12, 80)
	limits := ProcessLimits{
		MaxProcesses:       10,
		MaxDepth:           15,
		MaxStepsPerProcess: 32,
		MaxTotalSteps:      128,
	}

	first := discoverProcesses(nodes, edges, limits)
	second := discoverProcesses(nodes, edges, limits)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("bounded process discovery is not deterministic")
	}
	if !first.Truncated || first.TruncationReason != "step_limit" {
		t.Fatalf("truncation = %v %q, want step_limit", first.Truncated, first.TruncationReason)
	}
	if len(first.Processes) != 4 {
		t.Fatalf("processes = %d, want 4 at the total-step ceiling", len(first.Processes))
	}

	totalSteps := 0
	memberships := 0
	for _, process := range first.Processes {
		if len(process.Steps) > limits.MaxStepsPerProcess {
			t.Fatalf("process %s retained %d steps, limit %d", process.ID, len(process.Steps), limits.MaxStepsPerProcess)
		}
		if !process.Truncated {
			t.Fatalf("process %s should report its bounded prefix", process.ID)
		}
		totalSteps += len(process.Steps)
	}
	for _, processIDs := range first.NodeToProcs {
		memberships += len(processIDs)
	}
	if totalSteps > limits.MaxTotalSteps {
		t.Fatalf("retained %d steps, limit %d", totalSteps, limits.MaxTotalSteps)
	}
	if memberships != totalSteps {
		t.Fatalf("node-to-process memberships = %d, retained steps = %d", memberships, totalSteps)
	}
}

func TestTraceForwardBoundedHandlesCycles(t *testing.T) {
	callees := map[string][]string{
		"a": {"b"},
		"b": {"c"},
		"c": {"a"},
	}
	steps, truncated := traceForwardBounded("a", callees, 15, 8)
	if truncated {
		t.Fatal("finite cycle should complete without truncation")
	}
	want := []Step{{ID: "a", Depth: 0}, {ID: "b", Depth: 1}, {ID: "c", Depth: 2}}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("steps = %#v, want %#v", steps, want)
	}
}

func BenchmarkDiscoverProcessesBoundedDense(b *testing.B) {
	nodes, edges := processFixture(50, 4096)
	limits := defaultProcessLimits()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := discoverProcesses(nodes, edges, limits)
		if len(result.Processes) == 0 {
			b.Fatal("no processes discovered")
		}
	}
}
