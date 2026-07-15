package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/zzet/gortex/internal/contracts"
	"github.com/zzet/gortex/internal/graph"
)

type blockingContractNodeStore struct {
	graph.Store
}

func (s *blockingContractNodeStore) GetNode(string) *graph.Node {
	panic("contract impact must use the context-aware node lookup")
}

func (s *blockingContractNodeStore) GetNodeContext(ctx context.Context, _ string) (*graph.Node, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestComputeContractImpactContextStopsOnCancellation(t *testing.T) {
	const typeID = "repo/model.go::Payload"
	registry := contracts.NewRegistry()
	registry.Add(contracts.Contract{
		ID:   "GET /payload",
		Role: contracts.RoleProvider,
		Meta: map[string]any{"response_type": typeID},
	})
	registry.Add(contracts.Contract{
		ID:   "GET /payload",
		Role: contracts.RoleConsumer,
		Meta: map[string]any{"response_type": typeID},
	})

	server := &Server{
		graph:            &blockingContractNodeStore{Store: graph.New()},
		contractRegistry: registry,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	started := time.Now()
	if got := server.computeContractImpactContext(ctx, []string{typeID}); got != nil {
		t.Fatalf("cancelled contract enrichment = %#v, want nil", got)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("cancelled contract enrichment took %s; want <= 250ms", elapsed)
	}
}
