package store_sqlite

import (
	"database/sql"

	"github.com/zzet/gortex/internal/graph"
)

var (
	_ graph.BlameEnrichmentWriter = (*Store)(nil)
	_ graph.BlameEnrichmentReader = (*Store)(nil)
)

const blameChunk = 180

const blameCols = `node_id, repo_prefix, commit_sha, email, ts`

func (s *Store) BulkSetBlame(repoPrefix string, rows []graph.BlameEnrichment) error {
	if len(rows) == 0 {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	for start := 0; start < len(rows); start += blameChunk {
		end := start + blameChunk
		if end > len(rows) {
			end = len(rows)
		}
		batch := rows[start:end]
		args := make([]any, 0, len(batch)*5)
		stmt := make([]byte, 0, 96+len(batch)*16)
		stmt = append(stmt, "INSERT OR REPLACE INTO blame_enrichment ("...)
		stmt = append(stmt, blameCols...)
		stmt = append(stmt, ") VALUES "...)
		for i, e := range batch {
			if i > 0 {
				stmt = append(stmt, ',')
			}
			stmt = append(stmt, "(?,?,?,?,?)"...)
			args = append(args, e.NodeID, repoPrefix, e.Commit, e.Email, e.Timestamp)
		}
		if _, err := tx.Exec(string(stmt), args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteBlame(nodeIDs []string) error {
	if len(nodeIDs) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(nodeIDs))
	uniq := make([]string, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniq = append(uniq, id)
	}
	if len(uniq) == 0 {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	for start := 0; start < len(uniq); start += blameChunk {
		end := start + blameChunk
		if end > len(uniq) {
			end = len(uniq)
		}
		chunk := uniq[start:end]
		args := make([]any, len(chunk))
		stmt := make([]byte, 0, 56+len(chunk)*2)
		stmt = append(stmt, "DELETE FROM blame_enrichment WHERE node_id IN ("...)
		for i, id := range chunk {
			if i > 0 {
				stmt = append(stmt, ',')
			}
			stmt = append(stmt, '?')
			args[i] = id
		}
		stmt = append(stmt, ')')
		if _, err := tx.Exec(string(stmt), args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) BlameRows(repoPrefix string) []graph.BlameEnrichment {
	var (
		rows *sql.Rows
		err  error
	)
	if repoPrefix == "" {
		rows, err = s.db.Query(`SELECT ` + blameCols + ` FROM blame_enrichment`)
	} else {
		rows, err = s.db.Query(`SELECT `+blameCols+` FROM blame_enrichment WHERE repo_prefix = ?`, repoPrefix)
	}
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var out []graph.BlameEnrichment
	for rows.Next() {
		var e graph.BlameEnrichment
		if err := rows.Scan(&e.NodeID, &e.RepoPrefix, &e.Commit, &e.Email, &e.Timestamp); err != nil {
			return out
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return out
	}
	return out
}
