package analysis

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/zzet/gortex/internal/graph"
)

const (
	adversarialImpactFanIn    = 4096
	adversarialImpactRuns     = 5
	adversarialImpactTierCap  = 50
	adversarialImpactCallTime = 750 * time.Millisecond
)

// countingImpactStore models the bounded, context-aware SQLite read seam without
// involving the live daemon. The backing graph deliberately contains far more
// rows than impact is allowed to materialize.
type countingImpactStore struct {
	graph.Store

	inEdgeContextCalls int
	inEdgeLegacyCalls  int
	nodeContextCalls   int
	nodeLegacyCalls    int
	singleNodeCalls    int
	inEdgeRows         int
	nodeRows           int
	maxEdgeLimit       int
}

func (s *countingImpactStore) resetCounts() {
	s.inEdgeContextCalls = 0
	s.inEdgeLegacyCalls = 0
	s.nodeContextCalls = 0
	s.nodeLegacyCalls = 0
	s.singleNodeCalls = 0
	s.inEdgeRows = 0
	s.nodeRows = 0
	s.maxEdgeLimit = 0
}

func (s *countingImpactStore) GetNode(id string) *graph.Node {
	s.singleNodeCalls++
	return s.Store.GetNode(id)
}

func (s *countingImpactStore) GetNodeContext(ctx context.Context, id string) (*graph.Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.singleNodeCalls++
	return s.Store.GetNode(id), nil
}

func (s *countingImpactStore) GetNodesByIDs(ids []string) map[string]*graph.Node {
	s.nodeLegacyCalls++
	out := s.Store.GetNodesByIDs(ids)
	s.nodeRows += len(out)
	return out
}

func (s *countingImpactStore) GetNodesByIDsContext(ctx context.Context, ids []string) (map[string]*graph.Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.nodeContextCalls++
	out := s.Store.GetNodesByIDs(ids)
	s.nodeRows += len(out)
	return out, nil
}

func (s *countingImpactStore) GetInEdgesByNodeIDs(ids []string) map[string][]*graph.Edge {
	s.inEdgeLegacyCalls++
	out := s.Store.GetInEdgesByNodeIDs(ids)
	for _, edges := range out {
		s.inEdgeRows += len(edges)
	}
	return out
}

func (s *countingImpactStore) GetInEdgesByNodeIDsContext(ctx context.Context, ids []string, limit int) (map[string][]*graph.Edge, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if limit <= 0 {
		return nil, false, fmt.Errorf("impact requested an unbounded incoming-edge batch: %d", limit)
	}

	s.inEdgeContextCalls++
	if limit > s.maxEdgeLimit {
		s.maxEdgeLimit = limit
	}

	all := s.Store.GetInEdgesByNodeIDs(ids)
	out := make(map[string][]*graph.Edge, len(ids))
	remaining := limit
	truncated := false
	for _, id := range ids {
		edges := all[id]
		if len(edges) == 0 {
			continue
		}
		take := len(edges)
		if take > remaining {
			take = remaining
			truncated = true
		}
		if take > 0 {
			out[id] = append([]*graph.Edge(nil), edges[:take]...)
			s.inEdgeRows += take
			remaining -= take
		}
		if remaining == 0 {
			if take < len(edges) {
				truncated = true
			}
			continue
		}
	}
	return out, truncated, nil
}

func TestFillImpactLiveAdversarialFanInStaysBounded(t *testing.T) {
	store := newAdversarialImpactStore(t, adversarialImpactFanIn)

	for run := 0; run < adversarialImpactRuns; run++ {
		// Obtain the production-initialized result shape without performing work.
		initCtx, stopInit := context.WithCancel(context.Background())
		stopInit()
		result := AnalyzeImpactContext(initCtx, store, []string{"seed"}, nil, nil)
		store.resetCounts()

		ctx, cancel := context.WithTimeout(context.Background(), adversarialImpactCallTime)
		started := time.Now()
		fillImpactLive(ctx, store, result, []string{"seed"})
		elapsed := time.Since(started)
		err := ctx.Err()
		cancel()

		if err != nil {
			t.Fatalf("run %d exceeded %s: %v", run, adversarialImpactCallTime, err)
		}
		if elapsed >= adversarialImpactCallTime {
			t.Fatalf("run %d took %s; want below %s", run, elapsed, adversarialImpactCallTime)
		}
		if store.inEdgeContextCalls > 3 {
			t.Fatalf("run %d used %d incoming-edge batches; want at most 3", run, store.inEdgeContextCalls)
		}
		if store.nodeContextCalls > 3 {
			t.Fatalf("run %d used %d node batches; want at most 3", run, store.nodeContextCalls)
		}
		if store.inEdgeLegacyCalls != 0 || store.nodeLegacyCalls != 0 {
			t.Fatalf("run %d fell back to unbounded reads: edge=%d node=%d", run, store.inEdgeLegacyCalls, store.nodeLegacyCalls)
		}
		if store.maxEdgeLimit > adversarialImpactTierCap+1 {
			t.Fatalf("run %d requested edge limit %d; want at most %d", run, store.maxEdgeLimit, adversarialImpactTierCap+1)
		}
		if store.inEdgeRows > 3*(adversarialImpactTierCap+1) {
			t.Fatalf("run %d materialized %d edge rows; want at most %d", run, store.inEdgeRows, 3*(adversarialImpactTierCap+1))
		}
		if store.nodeRows > 3*adversarialImpactTierCap {
			t.Fatalf("run %d materialized %d node rows; want at most %d", run, store.nodeRows, 3*adversarialImpactTierCap)
		}
	}
}

func TestAnalyzeImpactContextAdversarialFanInIsConservative(t *testing.T) {
	store := newAdversarialImpactStore(t, adversarialImpactFanIn)
	ctx, cancel := context.WithTimeout(context.Background(), adversarialImpactCallTime)
	defer cancel()

	result := AnalyzeImpactContext(ctx, store, []string{"seed"}, nil, nil)
	if err := ctx.Err(); err != nil {
		t.Fatalf("impact did not finish within %s: %v", adversarialImpactCallTime, err)
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal impact result: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("decode impact result: %v", err)
	}
	if truncated, _ := fields["truncated"].(bool); !truncated {
		t.Fatalf("large fan-in result must report truncation: %s", encoded)
	}
	if risk, _ := fields["risk"].(string); risk == "" || risk == "LOW" {
		t.Fatalf("truncated large fan-in result must not be low risk: %s", encoded)
	}
}

func newAdversarialImpactStore(t *testing.T, fanIn int) *countingImpactStore {
	t.Helper()
	g := graph.New()
	g.AddNode(&graph.Node{ID: "seed", Name: "seed", Kind: graph.NodeKind("function"), FilePath: "core/seed.go"})

	for i := 0; i < fanIn; i++ {
		community := i % 32
		directID := fmt.Sprintf("direct-%05d", i)
		parentID := fmt.Sprintf("parent-%05d", i)
		rootID := fmt.Sprintf("root-%05d", i)
		basePath := fmt.Sprintf("community-%02d/pkg-%02d", community, i%128)

		g.AddNode(&graph.Node{ID: directID, Name: directID, Kind: graph.NodeKind("function"), FilePath: basePath + "/direct.go"})
		g.AddNode(&graph.Node{ID: parentID, Name: parentID, Kind: graph.NodeKind("function"), FilePath: basePath + "/parent.go"})
		g.AddNode(&graph.Node{ID: rootID, Name: rootID, Kind: graph.NodeKind("function"), FilePath: basePath + "/root.go"})
		g.AddEdge(&graph.Edge{From: directID, To: "seed", Kind: graph.EdgeKind("calls")})
		g.AddEdge(&graph.Edge{From: parentID, To: directID, Kind: graph.EdgeKind("calls")})
		g.AddEdge(&graph.Edge{From: rootID, To: parentID, Kind: graph.EdgeKind("calls")})
	}

	return &countingImpactStore{Store: g}
}
