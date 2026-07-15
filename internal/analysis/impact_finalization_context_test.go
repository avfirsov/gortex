package analysis

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

type failingImpactSeedBatchStore struct {
	graph.Store
}

func (s *failingImpactSeedBatchStore) GetNodesByIDsContext(context.Context, []string) (map[string]*graph.Node, error) {
	return nil, errors.New("seed batch unavailable")
}

func TestAnalyzeImpactContextSeedReadErrorRemainsConservative(t *testing.T) {
	const seedID = "repo/service.go::Handle"
	backing := graph.New()
	backing.AddNode(&graph.Node{ID: seedID, Name: "Handle", Kind: graph.KindFunction})
	store := &failingImpactSeedBatchStore{Store: backing}

	result := AnalyzeImpactContext(context.Background(), store, []string{seedID}, nil, nil)
	if !result.Truncated {
		t.Fatal("seed-node read error must mark impact truncated")
	}
	if !result.LowerBound {
		t.Fatal("seed-node read error must mark impact as a lower bound")
	}
	if result.Risk == RiskLow {
		t.Fatal("truncated impact must never retain LOW risk")
	}
	if !strings.Contains(result.Summary, "lower bound") {
		t.Fatalf("summary %q does not disclose the lower bound", result.Summary)
	}
}
