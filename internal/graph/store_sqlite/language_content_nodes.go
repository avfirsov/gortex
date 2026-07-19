package store_sqlite

import "github.com/zzet/gortex/internal/graph"

var _ graph.ContentNodeReader = (*Store)(nil)

// GetNodesByLanguage pushes the global language predicate into SQLite and
// returns full rows only for the semantic provider that requested them.
func (s *Store) GetNodesByLanguage(language string) []*graph.Node {
	if language == "" {
		return nil
	}
	return s.queryNodesSQL(
		`SELECT `+lookupNodeCols+` FROM nodes WHERE language = ? ORDER BY id`,
		language,
	)
}

// GetRepoContentNodes selects CONTENT sections for one exact repository. The
// promoted data_class column avoids decoding non-content Meta blobs.
func (s *Store) GetRepoContentNodes(repoPrefix string) []*graph.Node {
	return s.queryNodesSQL(
		`SELECT `+lookupNodeCols+` FROM nodes WHERE repo_prefix = ? AND kind = ? AND data_class = 'content' ORDER BY id`,
		repoPrefix, graph.KindDoc,
	)
}
