package indexer

import (
	"path/filepath"
	"sort"

	"github.com/zzet/gortex/internal/graph"
)

// markPendingEnrichFull records that the next deferred semantic pass must use
// the repository entry point. Full work dominates any queued file frontier.
func (idx *Indexer) markPendingEnrichFull() {
	idx.deferredEnrichMu.Lock()
	idx.deferredEnrichGeneration++
	idx.deferredEnrichFull = true
	idx.deferredEnrichFiles = nil
	idx.pendingEnrich.Store(true)
	idx.deferredEnrichMu.Unlock()
}

// markPendingEnrichFiles merges a known repo-scoped frontier. The complete set
// is dispatched to a batch-capable provider in one call; it is never expanded
// into an N+1 loop of per-file provider calls.
func (idx *Indexer) markPendingEnrichFiles(filePaths []string) {
	if len(filePaths) == 0 {
		return
	}

	idx.deferredEnrichMu.Lock()
	idx.deferredEnrichGeneration++
	if idx.pendingEnrich.Load() && !idx.deferredEnrichFull && len(idx.deferredEnrichFiles) == 0 {
		// A caller using the legacy atomic-only marker queued work whose scope is
		// unknown. Preserve it as a full pass instead of silently narrowing it.
		idx.deferredEnrichFull = true
	}
	if !idx.deferredEnrichFull {
		if idx.deferredEnrichFiles == nil {
			idx.deferredEnrichFiles = make(map[string]struct{}, len(filePaths))
		}
		for _, filePath := range filePaths {
			if filePath != "" {
				idx.deferredEnrichFiles[filePath] = struct{}{}
			}
		}
	}
	idx.pendingEnrich.Store(true)
	idx.deferredEnrichMu.Unlock()
}

// deferredEnrichScope snapshots pending work. An empty frontier is always
// treated as full for compatibility with older/direct pendingEnrich writers.
func (idx *Indexer) deferredEnrichScope() (filePaths []string, full bool, generation uint64) {
	idx.deferredEnrichMu.Lock()
	defer idx.deferredEnrichMu.Unlock()
	filePaths = make([]string, 0, len(idx.deferredEnrichFiles))
	for filePath := range idx.deferredEnrichFiles {
		filePaths = append(filePaths, filePath)
	}
	sort.Strings(filePaths)
	return filePaths,
		idx.deferredEnrichFull || len(filePaths) == 0,
		idx.deferredEnrichGeneration
}

// deferredEnrichFrontiers partitions one exact graph-file frontier by language
// with a single batched graph read. Deleted paths are absent and therefore need
// no provider work: their semantic nodes and edges were already evicted. The
// caller invokes each language provider once for the complete file batch.
func (idx *Indexer) deferredEnrichFrontiers(graphPaths []string) map[string][]string {
	nodesByFile := idx.graph.GetFileNodesByPaths(graphPaths)
	byLanguage := make(map[string][]string)
	for _, graphPath := range graphPaths {
		base := filepath.Base(graphPath)
		if base == "go.mod" || base == "go.work" {
			continue
		}
		for _, node := range nodesByFile[graphPath] {
			if node == nil || node.Kind != graph.KindFile || node.Language == "" {
				continue
			}
			byLanguage[node.Language] = append(byLanguage[node.Language], graphPath)
			break
		}
	}
	for language, paths := range byLanguage {
		byLanguage[language] = appendUniqueSorted(nil, paths...)
	}
	return byLanguage
}

// semanticDependencyFrontierForDeletedFiles captures the surviving source files
// whose resolvable references point at symbols about to be deleted. Every graph
// access is batched over the deletion set; the result is repo-local because a
// provider rooted at this indexer's checkout cannot safely enrich another repo.
func (idx *Indexer) semanticDependencyFrontierForDeletedFiles(relPaths []string) []string {
	if len(relPaths) == 0 {
		return nil
	}
	graphPaths := make([]string, 0, len(relPaths))
	for _, relPath := range relPaths {
		if relPath != "" {
			graphPaths = append(graphPaths, idx.prefixPath(filepath.FromSlash(relPath)))
		}
	}
	graphPaths = appendUniqueSorted(nil, graphPaths...)
	nodesByFile := idx.graph.GetFileNodesByPaths(graphPaths)
	evictedIDs := make(map[string]struct{})
	var nodeIDs []string
	for _, graphPath := range graphPaths {
		for _, node := range nodesByFile[graphPath] {
			if node == nil || node.ID == "" {
				continue
			}
			if _, duplicate := evictedIDs[node.ID]; duplicate {
				continue
			}
			evictedIDs[node.ID] = struct{}{}
			nodeIDs = append(nodeIDs, node.ID)
		}
	}
	if len(nodeIDs) == 0 {
		return nil
	}

	incoming := idx.graph.GetInEdgesByNodeIDs(nodeIDs)
	sourceSet := make(map[string]struct{})
	for _, nodeID := range nodeIDs {
		for _, edge := range incoming[nodeID] {
			if edge == nil || !graph.IsResolvableRefEdge(edge.Kind) || graph.IsUnresolvedTarget(edge.To) {
				continue
			}
			if _, deleted := evictedIDs[edge.From]; !deleted {
				sourceSet[edge.From] = struct{}{}
			}
		}
	}
	if len(sourceSet) == 0 {
		return nil
	}
	sourceIDs := make([]string, 0, len(sourceSet))
	for id := range sourceSet {
		sourceIDs = append(sourceIDs, id)
	}
	sources := idx.graph.GetNodesByIDs(sourceIDs)
	var frontier []string
	for _, id := range sourceIDs {
		node := sources[id]
		if node == nil || node.FilePath == "" || node.RepoPrefix != idx.repoPrefix {
			continue
		}
		frontier = append(frontier, node.FilePath)
	}
	return appendUniqueSorted(nil, frontier...)
}

// clearPendingEnrich clears only the work represented by generation. If a
// watcher queued another change during enrichment, that newer work remains.
func (idx *Indexer) clearPendingEnrich(generation uint64) bool {
	idx.deferredEnrichMu.Lock()
	defer idx.deferredEnrichMu.Unlock()
	if idx.deferredEnrichGeneration != generation {
		return false
	}
	idx.deferredEnrichFiles = nil
	idx.deferredEnrichFull = false
	idx.pendingEnrich.Store(false)
	return true
}
