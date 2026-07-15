package analysis

import (
	"context"
	"errors"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

type impactContextNodesStoreStub struct {
	graph.Store
	err    error
	called bool
}

func (s *impactContextNodesStoreStub) GetNodesByIDsContext(context.Context, []string) (map[string]*graph.Node, error) {
	s.called = true
	return nil, s.err
}

func TestGetImpactNodesContextUsesOptionalStoreMethod(t *testing.T) {
	s := &impactContextNodesStoreStub{err: context.Canceled}
	_, err := getImpactNodesContext(context.Background(), s, []string{"node"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("getImpactNodesContext error = %v, want context.Canceled", err)
	}
	if !s.called {
		t.Fatal("optional GetNodesByIDsContext was not called")
	}
}

type impactLegacyNodesStoreStub struct {
	graph.Store
	called bool
}

func (s *impactLegacyNodesStoreStub) GetNodesByIDs([]string) map[string]*graph.Node {
	s.called = true
	return nil
}

func TestGetImpactNodesContextChecksCancellationBeforeLegacyFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &impactLegacyNodesStoreStub{}

	_, err := getImpactNodesContext(ctx, s, []string{"node"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("getImpactNodesContext error = %v, want context.Canceled", err)
	}
	if s.called {
		t.Fatal("legacy GetNodesByIDs called after cancellation")
	}
}
