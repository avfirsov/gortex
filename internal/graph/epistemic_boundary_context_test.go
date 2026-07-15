package graph

import (
	"context"
	"testing"
)

type boundaryContextStoreStub struct {
	Store
	edges        map[string][]*Edge
	nodes        map[string]*Node
	edgeErr      error
	nodeErr      error
	edgeCalled   bool
	nodeCalled   bool
	legacyCalled bool
}

func (s *boundaryContextStoreStub) GetOutEdgesByNodeIDsContext(context.Context, []string, int) (map[string][]*Edge, bool, error) {
	s.edgeCalled = true
	return s.edges, false, s.edgeErr
}

func (s *boundaryContextStoreStub) GetNodesByIDsContext(context.Context, []string) (map[string]*Node, error) {
	s.nodeCalled = true
	return s.nodes, s.nodeErr
}

func (s *boundaryContextStoreStub) GetOutEdges(string) []*Edge {
	s.legacyCalled = true
	return nil
}

func (s *boundaryContextStoreStub) GetNodesByIDs([]string) map[string]*Node {
	s.legacyCalled = true
	return nil
}

func TestCallerBoundariesContextStopsBeforeStoreReadsWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &boundaryContextStoreStub{}

	boundaries, truncated := CallerBoundariesContext(ctx, s, []string{"seed"}, 1)
	if len(boundaries) != 0 || !truncated {
		t.Fatalf("CallerBoundariesContext = (%v, %v), want empty conservative result", boundaries, truncated)
	}
	if s.edgeCalled || s.nodeCalled || s.legacyCalled {
		t.Fatal("store read attempted after cancellation")
	}
}

func TestCallerBoundariesContextUsesCancellableBatchReads(t *testing.T) {
	s := &boundaryContextStoreStub{
		edges: map[string][]*Edge{
			"seed": {{From: "seed", To: "pkg::Trait.method", Kind: EdgeImplements}},
		},
		nodes: map[string]*Node{
			"seed": {ID: "seed", Name: "Run"},
		},
	}

	boundaries, truncated := CallerBoundariesContext(context.Background(), s, []string{"seed"}, 1)
	if truncated {
		t.Fatal("unexpected truncated result")
	}
	if len(boundaries) != 1 || boundaries[0].SeedName != "Run" {
		t.Fatalf("boundaries = %#v, want one boundary named Run", boundaries)
	}
	if !s.edgeCalled || !s.nodeCalled {
		t.Fatal("cancellable batch methods were not both used")
	}
	if s.legacyCalled {
		t.Fatal("legacy store read used despite context-aware methods")
	}
}
