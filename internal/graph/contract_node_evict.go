package graph

// ContractNodeBatchEvicter removes a bounded set of contract nodes and every
// incident edge in one backend operation. It is intentionally contract-specific:
// partial contract reconciliation must never use EvictFile and delete unrelated
// source symbols merely to retire stale synthetic contract IDs.
type ContractNodeBatchEvicter interface {
	EvictContractNodesByIDs(ids []string) (nodesRemoved, edgesRemoved int)
}

// EvictContractNodesByIDs dispatches only to the set-oriented capability.
// Production Graph and SQLite stores implement it; adapter stores report
// unsupported instead of hiding an N+1 point-delete fallback.
func EvictContractNodesByIDs(store Store, ids []string) (nodesRemoved, edgesRemoved int, supported bool) {
	if store == nil || len(ids) == 0 {
		return 0, 0, true
	}
	evicter, ok := store.(ContractNodeBatchEvicter)
	if !ok {
		return 0, 0, false
	}
	nodesRemoved, edgesRemoved = evicter.EvictContractNodesByIDs(ids)
	return nodesRemoved, edgesRemoved, true
}

// EvictContractNodesByIDs implements the bounded in-memory capability while
// holding all shard locks once. Unknown and non-contract IDs are ignored.
func (g *Graph) EvictContractNodesByIDs(ids []string) (nodesRemoved, edgesRemoved int) {
	if g == nil || len(ids) == 0 {
		return 0, 0
	}
	receiptActive := g.beginReceiptMutation()
	if receiptActive {
		defer g.endReceiptMutation()
	}
	g.markMutationReceiptsIncomplete()
	g.lockAllWrite()
	defer g.unlockAllWrite()

	seen := make(map[string]struct{}, len(ids))
	evicted := make(map[string]string)
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		shard := g.shardFor(id)
		node := shard.nodes[id]
		if node == nil || node.Kind != KindContract {
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

var _ ContractNodeBatchEvicter = (*Graph)(nil)
