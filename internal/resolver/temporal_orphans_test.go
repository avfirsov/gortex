package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestDetectTemporalOrphans(t *testing.T) {
	b := newTemporalTestGraph()
	// Workflow dispatches ChargeCard (resolves) and MissingActivity (broken).
	b.addGoFunc("svc/wf.go::OrderWorkflow", "OrderWorkflow", "svc/wf.go", "svc")
	b.addStubCall("svc/wf.go::OrderWorkflow", "activity", "ChargeCard", "svc/wf.go")
	b.addStubCall("svc/wf.go::OrderWorkflow", "activity", "MissingActivity", "svc/wf.go")
	// Registrations.
	b.addGoFunc("svc/act.go::ChargeCard", "ChargeCard", "svc/act.go", "svc")
	b.addGoFunc("svc/act.go::UnusedActivity", "UnusedActivity", "svc/act.go", "svc")
	b.addGoFunc("svc/main.go::setup", "setup", "svc/main.go", "svc")
	// Register edges must differ by line — the real extractor emits each
	// w.Register*() at its own line (edgeKey includes File+Line); the
	// addGoRegister helper pins line 5, which would dedup multiple
	// same-kind registrations from one function.
	reg := func(kind, name string, line int) {
		b.g.AddEdge(&graph.Edge{
			From: "svc/main.go::setup",
			To:   "unresolved::extern::go.temporal.io/sdk/worker::Register" + capitalise(kind),
			Kind: graph.EdgeCalls, FilePath: "svc/main.go", Line: line,
			Meta: map[string]any{"via": "temporal.register", "temporal_kind": kind, "temporal_name": name},
		})
	}
	reg("activity", "ChargeCard", 3)
	reg("activity", "UnusedActivity", 4)
	reg("workflow", "OrderWorkflow", 5)
	// A signal sent with no handler.
	b.g.AddEdge(&graph.Edge{
		From: "svc/wf.go::OrderWorkflow", To: "unresolved::extern::workflow::SignalExternalWorkflow",
		Kind: graph.EdgeCalls, FilePath: "svc/wf.go", Line: 9,
		Meta: map[string]any{"via": "temporal.signal-send", "temporal_kind": "signal", "temporal_name": "ghost-signal"},
	})

	ResolveTemporalCalls(b.g)
	rep := DetectTemporalOrphans(b.g)

	names := func(os []TemporalOrphan) map[string]bool {
		m := map[string]bool{}
		for _, o := range os {
			m[o.Name] = true
		}
		return m
	}
	require.True(t, names(rep.BrokenDispatch)["MissingActivity"], "MissingActivity must be a broken dispatch")
	assert.False(t, names(rep.BrokenDispatch)["ChargeCard"], "ChargeCard resolves, not broken")
	assert.True(t, names(rep.SignalNoHandler)["ghost-signal"], "ghost-signal has no handler")

	orphanAct := map[string]bool{}
	for _, id := range rep.OrphanActivity {
		orphanAct[id] = true
	}
	assert.True(t, orphanAct["svc/act.go::UnusedActivity"], "UnusedActivity is registered but never dispatched")
	assert.False(t, orphanAct["svc/act.go::ChargeCard"], "ChargeCard is dispatched, not orphan")
}
