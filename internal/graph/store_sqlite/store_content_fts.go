package store_sqlite

import (
	"database/sql"
	"strings"

	"github.com/zzet/gortex/internal/graph"
)

// This file implements graph.ContentSearcher on the SQLite backend using
// the content_fts FTS5 virtual table declared in schema.go — the
// dedicated, on-disk full-text index for CONTENT (data_class="content")
// section bodies, kept physically separate from symbol_fts so content
// text never reaches the symbol search or the code-oriented graph passes.
//
// Streamed build: WipeContent(repoPrefix) once at the start of a full
// index, AppendContent each content file's sections as they are parsed
// (no per-file wipe), then BuildContentIndex to merge segments.
// Incremental reindex of one content file is WipeContentFile +
// AppendContent.

// Compile-time assertion: *Store satisfies the content-search capability.
var _ graph.ContentSearcher = (*Store)(nil)

// contentInsertChunkRows bounds rows per multi-row INSERT. Each row binds
// 5 host params (node_id, repo_prefix, file_path, ordinal, body); 180 rows
// is 900 params, comfortably under SQLite's default 999-variable limit.
const contentInsertChunkRows = 180

// WipeContent removes a repo's content rows before a full rebuild. Empty
// repoPrefix wipes the whole table (single-repo / conformance behaviour).
func (s *Store) WipeContent(repoPrefix string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`DELETE FROM content_fts WHERE repo_prefix = ?`, repoPrefix)
	return err
}

// WipeContentFile removes one file's content rows — the incremental
// reindex path when a single content file changes.
func (s *Store) WipeContentFile(filePath string) error {
	if filePath == "" {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`DELETE FROM content_fts WHERE file_path = ?`, filePath)
	return err
}

// WipeContentFileInRepo removes ONE file's content rows scoped to a repo —
// the crash-safe full-index sibling of WipeContentFile (which keys on
// file_path alone and so would clobber a same-named file in another repo).
// A full index streams content per file: delete this file's prior rows,
// then AppendContent its fresh sections — so a mid-parse kill leaves a mix
// of old+new content per file rather than the empty table a repo-wide
// pre-wipe would leave behind. Empty filePath is a no-op.
func (s *Store) WipeContentFileInRepo(repoPrefix, filePath string) error {
	if filePath == "" {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`DELETE FROM content_fts WHERE repo_prefix = ? AND file_path = ?`, repoPrefix, filePath)
	return err
}

// DeleteContentFilesForRepoNotIn sweeps a repo's content rows down to keep —
// every content row whose file_path is absent from keep is deleted. keep is
// the set of files that actually STREAMED content sections in the walk just
// completed (each recorded as the same file_path form AppendContent wrote),
// NOT the set of files that merely survive on disk: a file can still exist
// yet stop producing content (doc emptied, classification changed), and a
// disk-based keep would protect its stale rows forever. Run once at the end
// of a successful full index (right after the authoritative mtime replace),
// it reaps vanished files and content->no-content transitions in one scan;
// the per-file WipeContentFileInRepo + AppendContent streaming build
// refreshes the files that still produce content. Together they replace the
// old repo-wide pre-wipe: a mid-parse kill no longer empties the content
// index, and stale rows are reaped only on the next clean completion.
// Empty keep is a deliberate no-op safety net — never wipe a whole repo from
// an empty set; a caller that legitimately ends a walk with zero content
// files calls WipeContent explicitly instead.
func (s *Store) DeleteContentFilesForRepoNotIn(repoPrefix string, keep map[string]struct{}) error {
	if len(keep) == 0 {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	// Enumerate the repo's content file paths, then delete only those not in
	// keep. Content files are a small subset (docs / PDFs / office), so the
	// DISTINCT scan + targeted deletes stay cheap and dodge a giant NOT IN
	// (...) bound-variable list. Rows are drained + closed before the delete
	// tx opens (no open read cursor while writing on the same connection).
	rows, err := s.db.Query(`SELECT DISTINCT file_path FROM content_fts WHERE repo_prefix = ?`, repoPrefix)
	if err != nil {
		return err
	}
	var vanished []string
	for rows.Next() {
		var fp string
		if err := rows.Scan(&fp); err != nil {
			_ = rows.Close()
			return err
		}
		if _, ok := keep[fp]; !ok {
			vanished = append(vanished, fp)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	if len(vanished) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // rollback after Commit is a no-op
	const chunk = 900
	for start := 0; start < len(vanished); start += chunk {
		end := minInt(start+chunk, len(vanished))
		batch := vanished[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, 0, len(batch)+1)
		args = append(args, repoPrefix)
		for _, fp := range batch {
			args = append(args, fp)
		}
		if _, err := tx.Exec(`DELETE FROM content_fts WHERE repo_prefix = ? AND file_path IN (`+placeholders+`)`, args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AppendContent inserts content rows for repoPrefix without wiping — the
// streamed per-file build path. Callers wipe (whole repo or one file)
// first. Rows with an empty NodeID are skipped.
func (s *Store) AppendContent(repoPrefix string, items []graph.ContentFTSItem) error {
	if len(items) == 0 {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	commit := false
	defer func() {
		if !commit {
			_ = tx.Rollback()
		}
	}()

	for start := 0; start < len(items); start += contentInsertChunkRows {
		end := minInt(start+contentInsertChunkRows, len(items))
		chunk := items[start:end]

		var b strings.Builder
		b.WriteString(`INSERT INTO content_fts (node_id, repo_prefix, file_path, ordinal, body) VALUES `)
		args := make([]any, 0, len(chunk)*5)
		for _, it := range chunk {
			if it.NodeID == "" {
				continue
			}
			if len(args) > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`(?,?,?,?,?)`)
			args = append(args, it.NodeID, repoPrefix, it.FilePath, it.Ordinal, it.Body)
		}
		if len(args) == 0 {
			continue
		}
		if _, err := tx.Exec(b.String(), args...); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	commit = true
	return nil
}

// BuildContentIndex opportunistically merges FTS5 segments (a read-latency
// improvement). Like BuildSymbolIndex it is a no-op for correctness — the
// FTS index is maintained incrementally on every insert — and idempotent.
func (s *Store) BuildContentIndex() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, _ = s.db.Exec(`INSERT INTO content_fts(content_fts) VALUES('optimize')`)
	return nil
}

// SearchContent runs a content query scoped to repoPrefix (empty = all
// repos) and returns hits ordered by descending relevance, each carrying a
// short snippet excerpt from the matched body. Reuses the symbol path's
// write-side tokeniser (buildFTSMatch) so the content corpus and queries
// agree on camelCase / path-separator splitting.
func (s *Store) SearchContent(query, repoPrefix string, limit int) ([]graph.ContentHit, error) {
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	match := s.buildFTSMatch(query)
	if match == "" {
		return nil, nil
	}

	var sb strings.Builder
	// snippet() over the body column (index 4): no highlight marks, an
	// ellipsis for elision, ~16 tokens of context. CAST(ordinal AS INTEGER)
	// forces integer affinity so the FTS5 text column scans cleanly into an
	// int.
	sb.WriteString(`SELECT node_id, file_path, CAST(ordinal AS INTEGER), snippet(content_fts, 4, '', '', '…', 16), bm25(content_fts) FROM content_fts WHERE content_fts MATCH ?`)
	args := []any{match}
	if repoPrefix != "" {
		sb.WriteString(` AND repo_prefix = ?`)
		args = append(args, repoPrefix)
	}
	sb.WriteString(` ORDER BY bm25(content_fts) LIMIT ?`)
	args = append(args, limit)

	rows, err := s.db.Query(sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hits []graph.ContentHit
	for rows.Next() {
		var (
			id, fp, snip string
			ordinal      int
			score        float64
		)
		if err := rows.Scan(&id, &fp, &ordinal, &snip, &score); err != nil {
			return nil, err
		}
		if id == "" {
			continue
		}
		// bm25() is negative-better in SQLite; negate so higher = better,
		// matching the ContentHit contract. Rows already arrive best-first.
		hits = append(hits, graph.ContentHit{
			NodeID:   id,
			FilePath: fp,
			Ordinal:  ordinal,
			Score:    -score,
			Snippet:  snip,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return hits, nil
}

// ScanContent streams every content row (scoped to repoPrefix; empty = all
// repos) to fn with its FULL body, read incrementally via a cursor so a
// consumer iterating hundreds of thousands of sections stays bounded. fn
// returns false to stop the scan early.
func (s *Store) ScanContent(repoPrefix string, fn func(nodeID, filePath, body string) bool) error {
	var rows *sql.Rows
	var err error
	if repoPrefix == "" {
		rows, err = s.db.Query(`SELECT node_id, file_path, body FROM content_fts`)
	} else {
		rows, err = s.db.Query(`SELECT node_id, file_path, body FROM content_fts WHERE repo_prefix = ?`, repoPrefix)
	}
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var nodeID, filePath, body string
		if err := rows.Scan(&nodeID, &filePath, &body); err != nil {
			return err
		}
		if !fn(nodeID, filePath, body) {
			return nil
		}
	}
	return rows.Err()
}
