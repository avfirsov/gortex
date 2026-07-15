package analysis

import (
	"sort"

	"github.com/zzet/gortex/internal/graph"
)

const (
	defaultBoundedAdjacencyDepth = 2
	defaultBoundedAdjacencyNodes = 4096
	defaultBoundedAdjacencyEdges = 16384
	boundedAdjacencyBatchSize    = 512
)

// BoundedAdjacencyStats reports the exact work and whether a hard boundary made
// the snapshot a lower bound. Callers can reduce confidence instead of silently
// treating a capped neighborhood as the complete graph.
type BoundedAdjacencyStats struct {
	NodeCount   int
	EdgeCount   int
	Depth       int
	NodeBatches int
	EdgeBatches int
	Truncated   bool
}

// BuildBoundedAdjacencySnapshot constructs a deterministic call/reference CSR
// from a candidate-seeded neighborhood. It is the interactive counterpart to
// BuildAdjacencySnapshot: bounded batch reads per depth and explicit node/edge
// caps prevent a normal search from materializing the whole graph.
func BuildBoundedAdjacencySnapshot(g graph.Reader, roots []string, depth, maxNodes, maxEdges int) (*AdjacencySnapshot, BoundedAdjacencyStats) {
	snap := &AdjacencySnapshot{index: map[string]int{}}
	stats := BoundedAdjacencyStats{}
	if g == nil || len(roots) == 0 {
		return snap, stats
	}
	if depth <= 0 {
		depth = defaultBoundedAdjacencyDepth
	}
	if maxNodes <= 0 {
		maxNodes = defaultBoundedAdjacencyNodes
	}
	if maxEdges <= 0 {
		maxEdges = defaultBoundedAdjacencyEdges
	}

	rootIDs := uniqueSortedIDs(roots)
	rootNodes, batches := boundedNodesByIDs(g, rootIDs)
	stats.NodeBatches += batches
	seen := make(map[string]struct{}, min(len(rootNodes), maxNodes))
	frontier := make([]string, 0, min(len(rootNodes), maxNodes))
	for _, id := range rootIDs {
		if rootNodes[id] == nil {
			continue
		}
		if len(seen) == maxNodes {
			stats.Truncated = true
			break
		}
		seen[id] = struct{}{}
		frontier = append(frontier, id)
	}

	edges := make([]*graph.Edge, 0, min(maxEdges, len(frontier)*4))
	for level := 0; level < depth && len(frontier) > 0 && len(edges) < maxEdges; level++ {
		stats.Depth = level + 1
		out, edgeBatches := boundedOutEdges(g, frontier)
		stats.EdgeBatches += edgeBatches
		candidates := make([]*graph.Edge, 0)
		targetIDs := make([]string, 0)
		for _, from := range frontier {
			for _, edge := range out[from] {
				if edge == nil || (edge.Kind != graph.EdgeCalls && edge.Kind != graph.EdgeReferences) {
					continue
				}
				candidates = append(candidates, edge)
				targetIDs = append(targetIDs, edge.To)
			}
		}
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].From != candidates[j].From {
				return candidates[i].From < candidates[j].From
			}
			if candidates[i].To != candidates[j].To {
				return candidates[i].To < candidates[j].To
			}
			return candidates[i].Kind < candidates[j].Kind
		})
		targetNodes, nodeBatches := boundedNodesByIDs(g, uniqueSortedIDs(targetIDs))
		stats.NodeBatches += nodeBatches
		nextSet := make(map[string]struct{})
		for i, edge := range candidates {
			if len(edges) == maxEdges {
				stats.Truncated = stats.Truncated || i < len(candidates)
				break
			}
			if targetNodes[edge.To] == nil {
				continue
			}
			if _, ok := seen[edge.To]; !ok {
				if len(seen) == maxNodes {
					stats.Truncated = true
					continue
				}
				seen[edge.To] = struct{}{}
				nextSet[edge.To] = struct{}{}
			}
			edges = append(edges, edge)
		}
		frontier = make([]string, 0, len(nextSet))
		for id := range nextSet {
			frontier = append(frontier, id)
		}
		sort.Strings(frontier)
	}
	if len(frontier) > 0 && stats.Depth == depth {
		stats.Truncated = true
	}

	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	index := make(map[string]int, len(ids))
	for i, id := range ids {
		index[id] = i
	}
	type link struct {
		to int
		w  float64
	}
	adj := make([][]link, len(ids))
	for _, edge := range edges {
		from, fromOK := index[edge.From]
		to, toOK := index[edge.To]
		if !fromOK || !toOK {
			continue
		}
		adj[from] = append(adj[from], link{to: to, w: graph.ProvenanceWeight(edge)})
	}
	offsets := make([]int32, len(ids)+1)
	total := 0
	for i := range adj {
		offsets[i] = int32(total)
		total += len(adj[i])
	}
	offsets[len(ids)] = int32(total)
	neighbors := make([]int32, 0, total)
	weights := make([]float64, 0, total)
	outWeight := make([]float64, len(ids))
	for i := range adj {
		row := adj[i]
		sort.Slice(row, func(a, b int) bool { return row[a].to < row[b].to })
		for _, edge := range row {
			neighbors = append(neighbors, int32(edge.to))
			weights = append(weights, edge.w)
			outWeight[i] += edge.w
		}
	}
	snap.ids = ids
	snap.index = index
	snap.offsets = offsets
	snap.neighbors = neighbors
	snap.weights = weights
	snap.outWeight = outWeight
	snap.pkgRoots = computePackageRoots(ids, offsets, neighbors, weights)
	stats.NodeCount = len(ids)
	stats.EdgeCount = len(neighbors)
	return snap, stats
}

func uniqueSortedIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func boundedNodesByIDs(g graph.Reader, ids []string) (map[string]*graph.Node, int) {
	out := make(map[string]*graph.Node, len(ids))
	batches := 0
	for start := 0; start < len(ids); start += boundedAdjacencyBatchSize {
		end := min(start+boundedAdjacencyBatchSize, len(ids))
		for id, node := range g.GetNodesByIDs(ids[start:end]) {
			out[id] = node
		}
		batches++
	}
	return out, batches
}

func boundedOutEdges(g graph.Reader, ids []string) (map[string][]*graph.Edge, int) {
	out := make(map[string][]*graph.Edge, len(ids))
	batches := 0
	for start := 0; start < len(ids); start += boundedAdjacencyBatchSize {
		end := min(start+boundedAdjacencyBatchSize, len(ids))
		for id, edges := range g.GetOutEdgesByNodeIDs(ids[start:end]) {
			out[id] = edges
		}
		batches++
	}
	return out, batches
}
