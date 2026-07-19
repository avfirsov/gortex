package graph

// ExactEdgeBatchRemover is an optional backend capability for deleting a
// bounded set of complete logical edge identities in one operation. Unlike
// RemoveEdge, it distinguishes sibling call sites that share from/to/kind but
// differ by file or line.
type ExactEdgeBatchRemover interface {
	RemoveEdgesExact(edges []*Edge) int
}

// RemoveEdgesExact uses the backend's set-oriented implementation when
// available. The compatibility fallback is reserved for adapter stores; the
// production Graph and SQLite stores both implement the capability.
func RemoveEdgesExact(store Store, edges []*Edge) int {
	if store == nil || len(edges) == 0 {
		return 0
	}
	if remover, ok := store.(ExactEdgeBatchRemover); ok {
		return remover.RemoveEdgesExact(edges)
	}
	removed := 0
	for _, edge := range edges {
		if edge != nil && store.RemoveEdge(edge.From, edge.To, edge.Kind) {
			removed++
		}
	}
	return removed
}

// RemoveEdgesExact deletes complete edge identities from the in-memory
// adjacency indexes. The loop is entirely in-process; disk backends provide a
// single-transaction implementation rather than inheriting it.
func (g *Graph) RemoveEdgesExact(edges []*Edge) int {
	if g == nil || len(edges) == 0 {
		return 0
	}
	receiptActive := g.beginReceiptMutation()
	if receiptActive {
		defer g.endReceiptMutation()
	}
	g.markMutationReceiptsIncomplete()

	removed := 0
	seen := make(map[edgeKey]struct{}, len(edges))
	for _, edge := range edges {
		if edge == nil {
			continue
		}
		identity := keyOf(edge)
		if _, duplicate := seen[identity]; duplicate {
			continue
		}
		seen[identity] = struct{}{}
		unlock := g.lockTwoWrite(edge.From, edge.To)
		sFrom := g.shardFor(edge.From)
		key := hashEdgeKey(identity)
		pos, ok := sFrom.outEdgeIdx[edge.From][key]
		if !ok || pos >= len(sFrom.outEdges[edge.From]) {
			unlock()
			continue
		}
		stored := sFrom.outEdges[edge.From][pos]
		var srcRepo string
		if src := sFrom.nodes[edge.From]; src != nil {
			srcRepo = src.RepoPrefix
		}
		removeEdgeFromBucket(sFrom.outEdges, sFrom.outEdgeKeys, sFrom.outEdgeIdx, edge.From, key)
		sTo := g.shardFor(edge.To)
		removeEdgeFromBucket(sTo.inEdges, sTo.inEdgeKeys, sTo.inEdgeIdx, edge.To, key)
		sFrom.repoEdgeRemove(srcRepo, stored)
		unlock()
		removed++
	}
	if removed > 0 {
		g.edgeMutGen.Add(uint64(removed))
	}
	return removed
}

var _ ExactEdgeBatchRemover = (*Graph)(nil)
