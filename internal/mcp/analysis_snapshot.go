package mcp

import (
	"sync"

	"github.com/zzet/gortex/internal/graph"
)

// analysisSnapshotStore gives one RunAnalysis pass a shared, immutable view of
// metadata-free bulk reads. SQLite decodes a fresh copy of every node and edge
// string on each scan. PageRank, HITS, Leiden, adjacency, and auto-concepts all
// retain IDs from those scans, so running them directly against the backend
// leaves several independent copies of the same strings live in their caches.
//
// The wrapper embeds the real store for point reads, counts, mutations, and
// full-node scans. Only the optional lightweight scanners are memoized. It is
// scoped to a single, analysisMu-serialized pass and discarded afterward.
// Returning the same read-only pointers is safe because every participating
// analyzer treats lightweight nodes and edges as immutable.
type analysisSnapshotStore struct {
	graph.Store

	nodesOnce sync.Once
	nodes     []*graph.Node

	edgesOnce sync.Once
	edges     []*graph.Edge
}

var (
	_ graph.Store            = (*analysisSnapshotStore)(nil)
	_ graph.NodeLightScanner = (*analysisSnapshotStore)(nil)
	_ graph.LightEdgeScanner = (*analysisSnapshotStore)(nil)
)

func newAnalysisSnapshotStore(store graph.Store) *analysisSnapshotStore {
	return &analysisSnapshotStore{Store: store}
}

func (s *analysisSnapshotStore) AllNodesLight() []*graph.Node {
	s.nodesOnce.Do(func() {
		s.nodes = graph.AllNodesLight(s.Store)
	})
	return s.nodes
}

func (s *analysisSnapshotStore) AllEdgesLight(kinds ...graph.EdgeKind) []*graph.Edge {
	s.edgesOnce.Do(func() {
		// Leiden needs all supported kinds and runs first in RunAnalysis.
		// Snapshot the superset once; later call/reference-only consumers
		// receive a cheap filtered view that shares endpoint strings.
		s.edges = graph.EdgesForKindsLight(s.Store)
	})
	if len(kinds) == 0 {
		return s.edges
	}

	want := make(map[graph.EdgeKind]struct{}, len(kinds))
	for _, kind := range kinds {
		if kind != "" {
			want[kind] = struct{}{}
		}
	}
	if len(want) == 0 {
		return nil
	}

	out := make([]*graph.Edge, 0, len(s.edges))
	for _, edge := range s.edges {
		if edge == nil {
			continue
		}
		if _, ok := want[edge.Kind]; ok {
			out = append(out, edge)
		}
	}
	return out
}
