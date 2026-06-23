package store_sqlite

import (
	"github.com/zzet/gortex/internal/graph"
)

// Compile-time assertions that the SQLite Store satisfies the optional
// per-file metadata persistence capability (the files sidecar feeding
// index_health's per-file parse-error / node-count rollup).
var (
	_ graph.FileMetaWriter = (*Store)(nil)
	_ graph.FileMetaReader = (*Store)(nil)
)

// fileMetaChunk bounds rows per multi-row INSERT (6 params/row; 80 rows =
// 480 host params, well under SQLite's 999 default).
const fileMetaChunk = 80

// SetFileMetas upserts per-file metadata rows for one repo prefix in a single
// transaction, chunked under the host-parameter limit. Idempotent on the
// (repo_prefix, file_path) primary key. Empty input is a no-op.
func (s *Store) SetFileMetas(repoPrefix string, rows []graph.FileMetaRow) error {
	if len(rows) == 0 {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // rollback after Commit is a no-op

	for start := 0; start < len(rows); start += fileMetaChunk {
		end := start + fileMetaChunk
		if end > len(rows) {
			end = len(rows)
		}
		batch := rows[start:end]
		args := make([]any, 0, len(batch)*6)
		stmt := make([]byte, 0, 96+len(batch)*24)
		stmt = append(stmt, "INSERT OR REPLACE INTO files (repo_prefix, file_path, content_hash, size, node_count, errors) VALUES "...)
		for i, r := range batch {
			if i > 0 {
				stmt = append(stmt, ',')
			}
			stmt = append(stmt, "(?, ?, ?, ?, ?, ?)"...)
			args = append(args, repoPrefix, r.FilePath, r.ContentHash, r.Size, r.NodeCount, r.Errors)
		}
		if _, err := tx.Exec(string(stmt), args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteFileMetasByFiles drops the metadata rows for the supplied files in one
// repo prefix, chunked into `file_path IN (…)` DELETEs. Empty input is a no-op.
func (s *Store) DeleteFileMetasByFiles(repoPrefix string, files []string) error {
	if len(files) == 0 {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // rollback after Commit is a no-op

	for start := 0; start < len(files); start += fileMetaChunk {
		end := start + fileMetaChunk
		if end > len(files) {
			end = len(files)
		}
		chunk := files[start:end]
		args := make([]any, 0, len(chunk)+1)
		args = append(args, repoPrefix)
		stmt := make([]byte, 0, 64+len(chunk)*2)
		stmt = append(stmt, "DELETE FROM files WHERE repo_prefix = ? AND file_path IN ("...)
		for i, f := range chunk {
			if i > 0 {
				stmt = append(stmt, ',')
			}
			stmt = append(stmt, '?')
			args = append(args, f)
		}
		stmt = append(stmt, ')')
		if _, err := tx.Exec(string(stmt), args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// FileMetasForRepo returns every recorded file row for the repo prefix.
// Always non-nil.
func (s *Store) FileMetasForRepo(repoPrefix string) ([]graph.FileMetaRow, error) {
	rows, err := s.db.Query(
		`SELECT file_path, content_hash, size, node_count, errors FROM files WHERE repo_prefix = ? ORDER BY file_path`,
		repoPrefix,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []graph.FileMetaRow{}
	for rows.Next() {
		var r graph.FileMetaRow
		if err := rows.Scan(&r.FilePath, &r.ContentHash, &r.Size, &r.NodeCount, &r.Errors); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
