package store_sqlite

import (
	"database/sql"
	"strings"

	"github.com/zzet/gortex/internal/graph"
)

type sqliteEdgeIdentity struct {
	from     string
	to       string
	kind     graph.EdgeKind
	filePath string
	line     int
}

func sqliteIdentityForEdge(e *graph.Edge) sqliteEdgeIdentity {
	return sqliteEdgeIdentity{
		from: e.From, to: e.To, kind: e.Kind, filePath: e.FilePath, line: e.Line,
	}
}

func (s *Store) edgeExistsLocked(e *graph.Edge) (bool, error) {
	var one int
	err := s.stmtEdgeExists.QueryRow(e.From, e.To, string(e.Kind), e.FilePath, e.Line).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// batchContainsNewEdgeLocked determines whether AddBatch can change topology.
// It runs only while an active analysis generation exists; ordinary indexing
// pays no preflight cost. Composite keys are checked in bounded queries so an
// idempotent enrichment batch preserves the expensive warm analysis cache.
func (s *Store) batchContainsNewEdgeLocked(edges []*graph.Edge) (bool, error) {
	const keysPerQuery = 180 // five bound values per key; remain below SQLite's 999 limit
	unique := make(map[sqliteEdgeIdentity]struct{}, len(edges))
	keys := make([]sqliteEdgeIdentity, 0, len(edges))
	for _, edge := range edges {
		if edge == nil || graph.IsProxyID(edge.From) || graph.IsProxyID(edge.To) {
			continue
		}
		key := sqliteIdentityForEdge(edge)
		if _, exists := unique[key]; exists {
			continue
		}
		unique[key] = struct{}{}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return false, nil
	}

	found := make(map[sqliteEdgeIdentity]struct{}, len(keys))
	for start := 0; start < len(keys); start += keysPerQuery {
		end := start + keysPerQuery
		if end > len(keys) {
			end = len(keys)
		}
		chunk := keys[start:end]
		clauses := make([]string, len(chunk))
		args := make([]any, 0, len(chunk)*5)
		for i, key := range chunk {
			clauses[i] = `(from_id = ? AND to_id = ? AND kind = ? AND file_path = ? AND line = ?)`
			args = append(args, key.from, key.to, string(key.kind), key.filePath, key.line)
		}
		rows, err := s.db.Query(
			`SELECT from_id, to_id, kind, file_path, line FROM edges WHERE `+strings.Join(clauses, " OR "),
			args...,
		)
		if err != nil {
			return false, err
		}
		for rows.Next() {
			var key sqliteEdgeIdentity
			var kind string
			if err := rows.Scan(&key.from, &key.to, &kind, &key.filePath, &key.line); err != nil {
				_ = rows.Close()
				return false, err
			}
			key.kind = graph.EdgeKind(kind)
			found[key] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return false, err
		}
		_ = rows.Close()
	}
	return len(found) != len(keys), nil
}
