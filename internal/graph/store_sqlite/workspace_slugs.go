package store_sqlite

import (
	"strings"

	"github.com/zzet/gortex/internal/graph"
)

var _ graph.WorkspaceSlugBackfiller = (*Store)(nil)

const workspaceSlugChunkSize = 300 // 900 host parameters, below SQLite's conservative 999 limit.

// BackfillWorkspaceSlugs fills legacy empty boundary columns with set-oriented
// VALUES updates. It neither reads node rows nor rewrites Meta blobs.
func (s *Store) BackfillWorkspaceSlugs(slugs []graph.WorkspaceSlug) int {
	updates := make([]graph.WorkspaceSlug, 0, len(slugs))
	positions := make(map[string]int, len(slugs))
	for _, slug := range slugs {
		if slug.RepoPrefix == "" || (slug.Workspace == "" && slug.Project == "") {
			continue
		}
		if pos, ok := positions[slug.RepoPrefix]; ok {
			updates[pos] = slug
			continue
		}
		positions[slug.RepoPrefix] = len(updates)
		updates = append(updates, slug)
	}
	if len(updates) == 0 {
		return 0
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.beginWrite()
	if err != nil {
		panicOnFatal(err)
		return 0
	}
	changedRows := 0
	for start := 0; start < len(updates); start += workspaceSlugChunkSize {
		end := minInt(start+workspaceSlugChunkSize, len(updates))
		query, args := workspaceSlugUpdate(updates[start:end])
		result, execErr := tx.Exec(query, args...)
		if execErr != nil {
			_ = tx.Rollback()
			panicOnFatal(execErr)
			return 0
		}
		changed, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			_ = tx.Rollback()
			panicOnFatal(rowsErr)
			return 0
		}
		changedRows += int(changed)
	}

	invalidatedAnalysis := false
	if changedRows > 0 && s.analysisGenerationPresent {
		if err := invalidateAnalysisGenerationTx(tx); err != nil {
			_ = tx.Rollback()
			panicOnFatal(err)
			return 0
		}
		invalidatedAnalysis = true
	}
	if err := tx.Commit(); err != nil {
		panicOnFatal(err)
		return 0
	}
	if invalidatedAnalysis {
		s.analysisGenerationPresent = false
	}
	s.finishAnalysisMutationLocked(changedRows > 0)
	return changedRows
}

func workspaceSlugUpdate(slugs []graph.WorkspaceSlug) (string, []any) {
	var values strings.Builder
	values.Grow(len(slugs) * len("(?,?,?),"))
	args := make([]any, 0, len(slugs)*3)
	for i, slug := range slugs {
		if i > 0 {
			values.WriteByte(',')
		}
		values.WriteString("(?,?,?)")
		args = append(args, slug.RepoPrefix, slug.Workspace, slug.Project)
	}
	query := `WITH updates(repo_prefix, workspace_id, project_id) AS (VALUES ` + values.String() + `)
	UPDATE nodes AS n
	SET workspace_id = CASE
			WHEN n.workspace_id = '' AND u.workspace_id <> '' THEN u.workspace_id
			ELSE n.workspace_id
		END,
		project_id = CASE
			WHEN n.project_id = '' AND u.project_id <> '' THEN u.project_id
			ELSE n.project_id
		END
	FROM updates AS u
	WHERE n.repo_prefix = u.repo_prefix
		AND ((n.workspace_id = '' AND u.workspace_id <> '')
			OR (n.project_id = '' AND u.project_id <> ''))`
	return query, args
}
