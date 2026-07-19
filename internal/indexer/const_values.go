package indexer

import (
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// persistConstValues writes a file's extracted constant literal values to
// the backend's constant_values sidecar (when it implements
// graph.ConstantValueWriter — the on-disk backend and the in-memory
// store both do). The resolver reads these to dereference a
// const-identifier Temporal dispatch name to its literal value across
// files.
//
// ExtractionResult.ConstValues carries pre-repo-prefix node ids / file
// paths (they are stamped at extraction time, before applyRepoPrefix
// rewrites the node ids). This helper replicates that same prefix
// transform so the persisted node_id matches the final graph node id the
// resolver looks up by, independent of when applyRepoPrefix ran. Each
// file's prior rows are deleted first so a reindex replaces them cleanly.
func (idx *Indexer) persistConstValues(result *parser.ExtractionResult) {
	rows, files := idx.prepareConstValues(result)
	persistConstantValueRows(idx.graph, idx.repoPrefix, files, rows)
}

func (idx *Indexer) prepareConstValues(result *parser.ExtractionResult) ([]graph.ConstantValueRow, []string) {
	if result == nil || len(result.ConstValues) == 0 {
		return nil, nil
	}
	prefix := ""
	if idx.repoPrefix != "" {
		prefix = idx.repoPrefix + "/"
	}
	rows := make([]graph.ConstantValueRow, 0, len(result.ConstValues))
	fileSet := make(map[string]struct{}, len(result.ConstValues))
	for _, cv := range result.ConstValues {
		rows = append(rows, graph.ConstantValueRow{
			NodeID:   prefix + cv.NodeID,
			FilePath: prefix + cv.FilePath,
			Value:    cv.Value,
		})
		fileSet[prefix+cv.FilePath] = struct{}{}
	}
	files := make([]string, 0, len(fileSet))
	for filePath := range fileSet {
		files = append(files, filePath)
	}
	return rows, files
}

func persistConstantValueRows(target graph.Store, repoPrefix string, files []string, rows []graph.ConstantValueRow) {
	cw, ok := target.(graph.ConstantValueWriter)
	if !ok || len(rows) == 0 {
		return
	}
	_ = cw.DeleteConstantValuesByFiles(repoPrefix, files)
	_ = cw.BulkSetConstantValues(repoPrefix, rows)
}
