package store_sqlite

import (
	"database/sql"
	"errors"

	"github.com/zzet/gortex/internal/graph"
)

// AnalysisMutationRevision is a process-local graph mutation clock. Durable
// restart correctness comes from clearing the active generation pointer before
// a committed graph mutation can become visible.
func (s *Store) AnalysisMutationRevision() uint64 {
	return s.analysisMutationRevision.Load()
}

// initAnalysisGenerationState makes interrupted builders collectible and
// initializes the mutation hot-path latch from the active singleton.
func (s *Store) initAnalysisGenerationState() error {
	if _, err := s.db.Exec(`UPDATE analysis_generations SET state = ? WHERE state = ?`, analysisGenerationStale, analysisGenerationBuilding); err != nil {
		return err
	}
	var present int
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM analysis_active_generation LIMIT 1)`).Scan(&present); err != nil {
		return err
	}
	s.analysisGenerationPresent = present != 0
	return nil
}

// CommitAnalysisSnapshot closes the revision-check-to-install race by holding
// the graph mutation gate across both operations. install must only publish
// in-memory pointers/tokens and must not re-enter graph mutation methods.
func (s *Store) CommitAnalysisSnapshot(expectedRevision uint64, install func()) bool {
	if install == nil {
		return false
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.analysisMutationRevision.Load() != expectedRevision {
		return false
	}
	install()
	return true
}

// invalidateAnalysisGenerationLocked commits durable invalidation before its
// caller mutates nodes or edges. Building generations are made collectible;
// the active singleton is cleared and its generation marked stale. A crash can
// therefore only lose an optimization, never resurrect stale analysis.
// writeMu must be held.
func (s *Store) invalidateAnalysisGenerationLocked() error {
	if !s.analysisGenerationPresent {
		return nil
	}
	tx, err := s.beginWrite()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.Exec(`UPDATE analysis_generations SET state = ? WHERE state = ? OR generation_id IN (SELECT generation_id FROM analysis_active_generation)`, analysisGenerationStale, analysisGenerationBuilding); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM analysis_active_generation`); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	s.analysisGenerationPresent = false
	return nil
}

// finishAnalysisMutationLocked advances the in-process race detector only
// after a graph mutation committed. writeMu must be held.
func (s *Store) finishAnalysisMutationLocked(changed bool) {
	if changed {
		s.analysisMutationRevision.Add(1)
	}
}

// invalidateAnalysisBeforeNodeMutationLocked preserves a generation across a
// metadata-only AddNode (reachability stamps are stored in Meta) while still
// treating every identity/location field read by AllNodesLight as relevant.
// In particular line/column shifts invalidate: consumers surface locations and
// must never restore old coordinates after restart. writeMu must be held.
func (s *Store) invalidateAnalysisBeforeNodeMutationLocked(n *graph.Node) bool {
	if !s.analysisGenerationPresent {
		return true
	}
	var (
		kind, name, qualName, filePath, language   string
		repoPrefix, workspaceID, projectID         string
		startLine, endLine, startColumn, endColumn int
		visibility                                 sql.NullString
		metaBlob                                   []byte
	)
	err := s.db.QueryRow(`SELECT kind, name, qual_name, file_path, start_line, end_line, start_column, end_column, language, repo_prefix, workspace_id, project_id, visibility, meta FROM nodes WHERE id = ?`, n.ID).Scan(
		&kind, &name, &qualName, &filePath,
		&startLine, &endLine, &startColumn, &endColumn,
		&language, &repoPrefix, &workspaceID, &projectID,
		&visibility, &metaBlob,
	)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		panicOnFatal(err)
		return false
	}
	lightChanged := errors.Is(err, sql.ErrNoRows) ||
		kind != string(n.Kind) || name != n.Name || qualName != n.QualName ||
		filePath != n.FilePath || startLine != n.StartLine || endLine != n.EndLine ||
		startColumn != n.StartColumn || endColumn != n.EndColumn ||
		language != n.Language || repoPrefix != n.RepoPrefix ||
		workspaceID != n.WorkspaceID || projectID != n.ProjectID
	processChanged := false
	if !errors.Is(err, sql.ErrNoRows) {
		storedMeta, decodeErr := decodeMeta(metaBlob)
		if decodeErr != nil {
			panicOnFatal(decodeErr)
			return false
		}
		storedEntry, _ := storedMeta["entry_point"].(bool)
		storedEntryKind, _ := storedMeta["entry_point_kind"].(string)
		newEntry, _ := n.Meta["entry_point"].(bool)
		newEntryKind, _ := n.Meta["entry_point_kind"].(string)
		newVisibility, _ := n.Meta["visibility"].(string)
		processChanged = visibility.String != newVisibility || storedEntry != newEntry ||
			(storedEntry && storedEntryKind != newEntryKind)
	}
	if !lightChanged && !processChanged {
		return true
	}
	return s.invalidateAnalysisBeforeMutationLocked()
}

// invalidateAnalysisBeforeMutationLocked is the common fail-closed gate for
// graph writes. If durable invalidation fails, callers must not apply the
// mutation: doing so could make stale analysis look valid after restart.
func (s *Store) invalidateAnalysisBeforeMutationLocked() bool {
	if err := s.invalidateAnalysisGenerationLocked(); err != nil {
		panicOnFatal(err)
		return false
	}
	return true
}
