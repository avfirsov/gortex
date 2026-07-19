package store_sqlite

import (
	"strings"

	"github.com/zzet/gortex/internal/graph"
)

const exactEdgeRemoveChunkSize = 160 // 160 * 5 = 800 bound parameters.

// RemoveEdgesExact deletes complete logical edge identities through bounded
// VALUES joins in one transaction. It is the set-oriented sibling of
// RemoveEdge and preserves same-endpoint sibling call sites.
func (s *Store) RemoveEdgesExact(edges []*graph.Edge) int {
	if len(edges) == 0 {
		return 0
	}
	type key struct {
		from, to, kind, file string
		line                 int
	}
	keys := make([]key, 0, len(edges))
	seen := make(map[key]struct{}, len(edges))
	for _, edge := range edges {
		if edge == nil {
			continue
		}
		k := key{edge.From, edge.To, string(edge.Kind), edge.FilePath, edge.Line}
		if _, duplicate := seen[k]; duplicate {
			continue
		}
		seen[k] = struct{}{}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return 0
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if !s.invalidateAnalysisBeforeMutationLocked() {
		return 0
	}
	tx, err := s.beginWrite()
	if err != nil {
		panicOnFatal(err)
		return 0
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	removed := 0
	for start := 0; start < len(keys); start += exactEdgeRemoveChunkSize {
		end := minInt(start+exactEdgeRemoveChunkSize, len(keys))
		chunk := keys[start:end]
		var values strings.Builder
		args := make([]any, 0, len(chunk)*5)
		for i, k := range chunk {
			if i > 0 {
				values.WriteByte(',')
			}
			values.WriteString("(?,?,?,?,?)")
			args = append(args, k.from, k.to, k.kind, k.file, k.line)
		}
		result, execErr := tx.Exec(`WITH wanted(from_id, to_id, kind, file_path, line) AS (VALUES `+values.String()+`)
DELETE FROM edges
WHERE EXISTS (
    SELECT 1 FROM wanted AS w
    WHERE w.from_id = edges.from_id
      AND w.to_id = edges.to_id
      AND w.kind = edges.kind
      AND w.file_path = edges.file_path
      AND w.line = edges.line
)`, args...)
		if execErr != nil {
			panicOnFatal(execErr)
			return 0
		}
		rows, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			panicOnFatal(rowsErr)
			return 0
		}
		removed += int(rows)
	}
	if err := tx.Commit(); err != nil {
		panicOnFatal(err)
		return 0
	}
	committed = true
	changed := removed > 0
	s.finishAnalysisMutationLocked(changed)
	if changed {
		s.markMutationReceiptsIncompleteLocked()
	}
	return removed
}

var _ graph.ExactEdgeBatchRemover = (*Store)(nil)
