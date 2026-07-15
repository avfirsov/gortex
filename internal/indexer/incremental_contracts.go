package indexer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zzet/gortex/internal/contracts"
	"github.com/zzet/gortex/internal/graph"
)

// refreshContractsForFiles re-extracts only the exact changed-file frontier.
// It returns whether the effective contract set changed and whether a
// conservative full-repo fallback was required.
func (idx *Indexer) refreshContractsForFiles(files []string) (bool, bool) {
	files = appendUniqueSorted(nil, files...)
	if len(files) == 0 {
		return false, false
	}
	if idx.contractRegistry == nil {
		idx.extractContracts()
		return true, true
	}

	reg := idx.contractRegistry
	_, byLang := idx.buildPerFileContractExtractors()
	changed := false
	for _, graphPath := range files {
		if contractRefreshAlwaysFull(graphPath) {
			idx.extractContracts()
			return true, true
		}

		prior := reg.ByFile(graphPath)
		fresh, mtimeNano, exists, fullFallback := idx.extractContractsForGraphFile(graphPath, byLang)
		if fullFallback {
			idx.extractContracts()
			return true, true
		}
		if !contractSetsEqual(prior, fresh) {
			reg.ReplaceFile(graphPath, fresh)
			changed = true
		}

		idx.contractCacheMu.Lock()
		if exists {
			idx.contractCache[graphPath] = &contractCacheEntry{mtimeNano: mtimeNano, contracts: fresh}
		} else {
			delete(idx.contractCache, graphPath)
		}
		idx.contractCacheMu.Unlock()
	}
	if changed {
		idx.commitContracts(reg)
	}
	return changed, false
}

func (idx *Indexer) extractContractsForGraphFile(
	graphPath string,
	byLang map[string][]contracts.Extractor,
) ([]contracts.Contract, int64, bool, bool) {
	fileNodes := idx.graph.GetFileNodes(graphPath)
	var fileNode *graph.Node
	for _, node := range fileNodes {
		if node != nil && node.Kind == graph.KindFile {
			fileNode = node
			break
		}
	}
	if fileNode == nil {
		// The exact file was deleted and its graph nodes were already evicted.
		return nil, 0, false, false
	}

	relPath := graphPath
	if idx.repoPrefix != "" {
		prefix := idx.repoPrefix + "/"
		if !strings.HasPrefix(relPath, prefix) {
			return nil, 0, true, true
		}
		relPath = strings.TrimPrefix(relPath, prefix)
	}
	absPath := filepath.Join(idx.rootPath, filepath.FromSlash(relPath))
	info, err := os.Stat(absPath)
	if err != nil {
		return nil, 0, true, true
	}
	src, err := os.ReadFile(absPath)
	if err != nil {
		return nil, 0, true, true
	}
	if contractSourceNeedsFullRefresh(graphPath, fileNode.Language, src) {
		return nil, 0, true, true
	}

	fileEdges := idx.graph.GetOutEdges(fileNode.ID)
	tree := contracts.ParseTreeForLang(fileNode.Language, src)
	fresh := idx.runContractExtractorsForFile(
		graphPath, src, fileNodes, fileEdges, byLang[fileNode.Language], tree,
	)
	if tree != nil {
		tree.Release()
	}

	// DI contracts are normally appended by the repo-wide post-pass. Rebuild
	// only those whose source edge belongs to this file so ReplaceFile does not
	// discard valid @Inject / provider records on an incremental refresh.
	for _, node := range fileNodes {
		if node == nil {
			continue
		}
		for _, edge := range idx.graph.GetOutEdges(node.ID) {
			contract, ok := diContractFromEdge(edge)
			if !ok || contract.FilePath != graphPath {
				continue
			}
			contract.RepoPrefix = idx.repoPrefix
			if idx.workspaceID != "" {
				contract.WorkspaceID = idx.workspaceID
			}
			if idx.projectID != "" {
				contract.ProjectID = idx.projectID
			}
			fresh = append(fresh, contract)
		}
	}
	return fresh, info.ModTime().UnixNano(), true, false
}

func contractRefreshAlwaysFull(graphPath string) bool {
	base := strings.ToLower(filepath.Base(graphPath))
	return base == "go.mod" || base == "go.work"
}

func contractSourceNeedsFullRefresh(graphPath, language string, src []byte) bool {
	lowerPath := strings.ToLower(graphPath)
	lowerSource := strings.ToLower(string(src))
	// These constructs can rewrite contracts owned by sibling files. They are
	// uncommon, so retain the full pass only when the changed bytes actually
	// contain a cross-file mount or DI declaration.
	if language == "python" && strings.Contains(lowerSource, "include_router") {
		return true
	}
	if (language == "typescript" || language == "javascript") &&
		(strings.Contains(lowerSource, ".use(") || strings.Contains(lowerSource, "@controller(")) {
		return true
	}
	if language == "java" &&
		(strings.Contains(lowerSource, "@bean") || strings.Contains(lowerSource, "@inject") ||
			strings.Contains(lowerSource, "@configuration")) {
		return true
	}
	return strings.HasSuffix(lowerPath, ".properties") || strings.HasSuffix(lowerPath, ".yaml") || strings.HasSuffix(lowerPath, ".yml")
}

func contractSetsEqual(left, right []contracts.Contract) bool {
	rows := func(list []contracts.Contract) ([]string, bool) {
		out := make([]string, 0, len(list))
		for _, contract := range list {
			encoded, err := json.Marshal(contract)
			if err != nil {
				return nil, false
			}
			out = append(out, string(encoded))
		}
		sort.Strings(out)
		return out, true
	}
	leftRows, leftOK := rows(left)
	rightRows, rightOK := rows(right)
	if !leftOK || !rightOK || len(leftRows) != len(rightRows) {
		return false
	}
	for i := range leftRows {
		if leftRows[i] != rightRows[i] {
			return false
		}
	}
	return true
}
