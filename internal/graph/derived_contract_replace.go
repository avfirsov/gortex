package graph

import "errors"

// DerivedContractReplacement replaces one exact incremental contract frontier.
// RemoveEdges carries complete logical edge identities; RemoveBridgeNodeIDs and
// TouchedTopicNodeIDs are bounded synthetic-node sets, never whole-kind scans.
type DerivedContractReplacement struct {
	RemoveEdges         []*Edge
	RemoveBridgeNodeIDs []string
	Nodes               []*Node
	Edges               []*Edge
	TouchedTopicNodeIDs []string
}

// DerivedContractReplaceResult reports actual or bounded backend mutations.
type DerivedContractReplaceResult struct {
	EdgesRemoved int
	NodesRemoved int
	NodesChanged int
	EdgesAdded   int
}

// DerivedContractReplacer atomically applies a derived frontier when supported.
// SQLite implements one transaction; Graph provides a set-oriented compatibility
// implementation for tests and the in-memory backend's remaining lifetime.
type DerivedContractReplacer interface {
	ReplaceDerivedContracts(replacement DerivedContractReplacement) (DerivedContractReplaceResult, error)
}

// ReplaceDerivedContracts refuses unbounded emulation. Production backends
// implement the exact capability so callers never fall back to AllNodes,
// AllEdges, or an edge-shaped point mutation loop.
func ReplaceDerivedContracts(store Store, replacement DerivedContractReplacement) (DerivedContractReplaceResult, error) {
	if store == nil {
		return DerivedContractReplaceResult{}, nil
	}
	replacer, ok := store.(DerivedContractReplacer)
	if !ok {
		return DerivedContractReplaceResult{}, errors.New("derived contract replacement unsupported")
	}
	return replacer.ReplaceDerivedContracts(replacement)
}

// ReplaceDerivedContracts implements the bounded in-memory path. reconcileMu
// serializes callers; SQLite supplies the transactionally atomic production
// implementation.
func (g *Graph) ReplaceDerivedContracts(replacement DerivedContractReplacement) (DerivedContractReplaceResult, error) {
	if g == nil {
		return DerivedContractReplaceResult{}, nil
	}
	result := DerivedContractReplaceResult{}
	if len(replacement.RemoveEdges) > 0 {
		result.EdgesRemoved = g.RemoveEdgesExact(replacement.RemoveEdges)
	}
	if len(replacement.RemoveBridgeNodeIDs) > 0 {
		nodes, edges := g.evictSyntheticNodesByIDs(
			replacement.RemoveBridgeNodeIDs,
			map[NodeKind]struct{}{KindContractBridge: {}},
		)
		result.NodesRemoved += nodes
		result.EdgesRemoved += edges
	}
	if len(replacement.Nodes) > 0 || len(replacement.Edges) > 0 {
		g.AddBatch(replacement.Nodes, replacement.Edges)
		result.NodesChanged = len(replacement.Nodes)
		result.EdgesAdded = len(replacement.Edges)
	}

	topicIDs := uniqueDerivedContractIDs(replacement.TouchedTopicNodeIDs)
	if len(topicIDs) == 0 {
		return result, nil
	}
	nodes := g.GetNodesByIDs(topicIDs)
	incoming := g.GetInEdgesByNodeIDs(topicIDs)
	orphanIDs := make([]string, 0, len(topicIDs))
	for _, id := range topicIDs {
		node := nodes[id]
		if node == nil || node.Kind != KindTopic {
			continue
		}
		owned := false
		for _, edge := range incoming[id] {
			if edge != nil && (edge.Kind == EdgeProducesTopic || edge.Kind == EdgeConsumesTopic) {
				owned = true
				break
			}
		}
		if !owned {
			orphanIDs = append(orphanIDs, id)
		}
	}
	if len(orphanIDs) > 0 {
		removedNodes, removedEdges := g.evictSyntheticNodesByIDs(
			orphanIDs, map[NodeKind]struct{}{KindTopic: {}},
		)
		result.NodesRemoved += removedNodes
		result.EdgesRemoved += removedEdges
	}
	return result, nil
}

func (g *Graph) evictSyntheticNodesByIDs(ids []string, kinds map[NodeKind]struct{}) (nodesRemoved, edgesRemoved int) {
	if g == nil || len(ids) == 0 || len(kinds) == 0 {
		return 0, 0
	}
	receiptActive := g.beginReceiptMutation()
	if receiptActive {
		defer g.endReceiptMutation()
	}
	g.markMutationReceiptsIncomplete()
	g.lockAllWrite()
	defer g.unlockAllWrite()

	evicted := make(map[string]string)
	for _, id := range uniqueDerivedContractIDs(ids) {
		shard := g.shardFor(id)
		node := shard.nodes[id]
		if node == nil {
			continue
		}
		if _, allowed := kinds[node.Kind]; !allowed {
			continue
		}
		evicted[id] = node.RepoPrefix
		shard.repoNodeRemove(node)
		delete(shard.nodes, id)
		if node.QualName != "" {
			if current, ok := shard.byQual[node.QualName]; ok && current.ID == id {
				delete(shard.byQual, node.QualName)
			}
		}
		removeNodeFromBucket(shard.byName, shard.byNameIdx, node.Name, id)
		removeNodeFromBucket(shard.byFile, shard.byFileIdx, node.FilePath, id)
		removeNodeFromBucket(shard.byRepo, shard.byRepoIdx, node.RepoPrefix, id)
	}
	if len(evicted) == 0 {
		return 0, 0
	}
	return len(evicted), g.evictEdgesLocked(evicted)
}

func uniqueDerivedContractIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

var _ DerivedContractReplacer = (*Graph)(nil)
