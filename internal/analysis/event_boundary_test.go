package analysis

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
)

// buildEventGraph wires one producer that emits topic "orders" and, optionally,
// a consumer that listens on it.
func buildEventGraph(withConsumer bool) (*graph.Graph, string) {
	g := graph.New()
	g.AddNode(&graph.Node{ID: "svc/pub.go::Publish", Name: "Publish", Kind: graph.KindFunction, FilePath: "svc/pub.go"})
	g.AddNode(&graph.Node{ID: "topic::orders", Name: "orders", Kind: graph.KindEvent, FilePath: "svc/pub.go"})
	g.AddEdge(&graph.Edge{From: "svc/pub.go::Publish", To: "topic::orders", Kind: graph.EdgeEmits})
	if withConsumer {
		g.AddNode(&graph.Node{ID: "worker/sub.go::Consume", Name: "Consume", Kind: graph.KindFunction, FilePath: "worker/sub.go"})
		g.AddEdge(&graph.Edge{From: "worker/sub.go::Consume", To: "topic::orders", Kind: graph.EdgeListensOn})
	}
	return g, "svc/pub.go::Publish"
}

func TestEventBoundaryProducerPath(t *testing.T) {
	g, pubID := buildEventGraph(true)
	fam := EventBoundaryFamily{Rules: []config.EventRule{{
		Name: "orders-producer", Topic: "orders",
		Producer: "ingest/**", Severity: "error",
	}}}
	v := fam.Evaluate(g, []string{pubID})
	require.Len(t, v, 1)
	require.Equal(t, "event_boundary", v[0].Kind)
	require.Equal(t, "error", v[0].Severity)
}

func TestEventBoundaryRequireConsumer(t *testing.T) {
	// No consumer present -> require_consumer fires.
	g, pubID := buildEventGraph(false)
	fam := EventBoundaryFamily{Rules: []config.EventRule{{
		Name: "orders-needs-consumer", Topic: "orders", RequireConsumer: true,
	}}}
	require.Len(t, fam.Evaluate(g, []string{pubID}), 1)

	// With a consumer present -> no violation.
	g2, pubID2 := buildEventGraph(true)
	require.Empty(t, fam.Evaluate(g2, []string{pubID2}))
}

func TestEventBoundaryForbid(t *testing.T) {
	g, pubID := buildEventGraph(true)
	fam := EventBoundaryFamily{Rules: []config.EventRule{{
		Name: "no-pub-from-svc", Topic: "*",
		Forbid: []string{"svc/**"},
	}}}
	v := fam.Evaluate(g, []string{pubID})
	require.Len(t, v, 1)
	require.Equal(t, "warn", v[0].Severity) // default severity
}

func TestEventBoundaryAllowedProducerClean(t *testing.T) {
	g, pubID := buildEventGraph(true)
	fam := EventBoundaryFamily{Rules: []config.EventRule{{
		Name: "orders-producer", Topic: "orders",
		Producer: "svc/**", // pub.go is in svc/, so it's allowed
	}}}
	require.Empty(t, fam.Evaluate(g, []string{pubID}))
}
