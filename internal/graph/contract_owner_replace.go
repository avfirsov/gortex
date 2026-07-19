package graph

import (
	"errors"
	"sort"
)

// ContractOwnerReplacement replaces contract ownership rows emitted by an
// exact repository/file frontier. TouchedNodeIDs includes both prior and current
// canonical contract IDs so orphan pruning handles rename and deletion.
type ContractOwnerReplacement struct {
	RepoPrefix     string
	FilePaths      []string
	TouchedNodeIDs []string
	Nodes          []*Node
	Edges          []*Edge
}

// ContractOwnerReplaceResult reports actual set-oriented mutations.
type ContractOwnerReplaceResult struct {
	EdgesRemoved int
	NodesRemoved int
	NodesChanged int
	EdgesAdded   int
}

// ContractOwnerReplacer applies a replacement atomically when the backend can.
// SQLite implements one transaction; the in-memory compatibility path below is
// still set-oriented and never removes another repository's shared-ID edges.
type ContractOwnerReplacer interface {
	ReplaceContractOwners(replacement ContractOwnerReplacement) (ContractOwnerReplaceResult, error)
}

// ReplaceContractOwners selects the native atomic capability or a bounded
// adapter fallback. The fallback performs one repo/kind projection, one target
// node batch, one exact edge removal batch, one AddBatch, and one batched orphan
// check. It never scans all nodes or issues edge-shaped point queries.
func ReplaceContractOwners(store Store, replacement ContractOwnerReplacement) (ContractOwnerReplaceResult, error) {
	if store == nil {
		return ContractOwnerReplaceResult{}, nil
	}
	if replacer, ok := store.(ContractOwnerReplacer); ok {
		return replacer.ReplaceContractOwners(replacement)
	}

	files := make(map[string]struct{}, len(replacement.FilePaths))
	for _, filePath := range replacement.FilePaths {
		if filePath != "" {
			files[filePath] = struct{}{}
		}
	}
	var candidates []*Edge
	var targetIDs []string
	for _, row := range ReadRepoEdgesByKinds(store, []string{replacement.RepoPrefix}, []EdgeKind{
		EdgeProvides, EdgeConsumes, EdgeHandlesRoute,
	}) {
		if row.Edge == nil {
			continue
		}
		if _, ok := files[row.Edge.FilePath]; !ok {
			continue
		}
		candidates = append(candidates, row.Edge)
		targetIDs = append(targetIDs, row.Edge.To)
	}
	targets := store.GetNodesByIDs(targetIDs)
	stale := make([]*Edge, 0, len(candidates))
	for _, edge := range candidates {
		if target := targets[edge.To]; target != nil && target.Kind == KindContract {
			stale = append(stale, edge)
		}
	}

	pruneIDs := contractOwnerPruneIDs(replacement)
	remover, canRemove := store.(ExactEdgeBatchRemover)
	if !canRemove && len(stale) > 0 {
		return ContractOwnerReplaceResult{}, errors.New("exact edge batch removal unsupported")
	}
	if _, canEvict := store.(ContractNodeBatchEvicter); !canEvict && len(pruneIDs) > 0 {
		return ContractOwnerReplaceResult{}, errors.New("contract node batch eviction unsupported")
	}

	result := ContractOwnerReplaceResult{}
	if len(stale) > 0 {
		result.EdgesRemoved = remover.RemoveEdgesExact(stale)
	}
	if len(replacement.Nodes) > 0 || len(replacement.Edges) > 0 {
		store.AddBatch(replacement.Nodes, replacement.Edges)
		result.NodesChanged = len(replacement.Nodes)
		result.EdgesAdded = len(replacement.Edges)
	}
	if len(pruneIDs) == 0 {
		return result, nil
	}

	incoming := store.GetInEdgesByNodeIDs(pruneIDs)
	orphanIDs := make([]string, 0, len(pruneIDs))
	for _, id := range pruneIDs {
		owned := false
		for _, edge := range incoming[id] {
			if edge != nil && (edge.Kind == EdgeProvides || edge.Kind == EdgeConsumes || edge.Kind == EdgeHandlesRoute) {
				owned = true
				break
			}
		}
		if !owned {
			orphanIDs = append(orphanIDs, id)
		}
	}
	if len(orphanIDs) > 0 {
		nodes, edges, _ := EvictContractNodesByIDs(store, orphanIDs)
		result.NodesRemoved = nodes
		result.EdgesRemoved += edges
	}
	return result, nil
}

func contractOwnerPruneIDs(replacement ContractOwnerReplacement) []string {
	current := make(map[string]struct{}, len(replacement.Nodes))
	for _, node := range replacement.Nodes {
		if node != nil && node.ID != "" {
			current[node.ID] = struct{}{}
		}
	}
	seen := make(map[string]struct{}, len(replacement.TouchedNodeIDs))
	for _, id := range replacement.TouchedNodeIDs {
		if id == "" {
			continue
		}
		if _, keep := current[id]; keep {
			continue
		}
		seen[id] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
