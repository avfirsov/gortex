package store_sqlite

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

func contentReplacement(filePath, token string, count int) graph.ContentFTSFileReplacement {
	items := make([]graph.ContentFTSItem, count)
	for i := range items {
		items[i] = graph.ContentFTSItem{
			NodeID:   fmt.Sprintf("%s::%d", filePath, i),
			FilePath: "caller-path-must-not-win",
			Ordinal:  i,
			Body:     fmt.Sprintf("%s section %d", token, i),
		}
	}
	return graph.ContentFTSFileReplacement{FilePath: filePath, Items: items}
}

func assertContentFTSIntegrity(t *testing.T, store *Store) {
	t.Helper()
	var result string
	require.NoError(t, store.db.QueryRow(`PRAGMA integrity_check`).Scan(&result))
	require.Equal(t, "ok", result)
	rows, err := store.db.Query(`PRAGMA foreign_key_check`)
	require.NoError(t, err)
	defer rows.Close()
	require.False(t, rows.Next())
	require.NoError(t, rows.Err())

	var ftsRows, ownerRows, exactRows int
	require.NoError(t, store.db.QueryRow(`SELECT COUNT(*) FROM content_fts`).Scan(&ftsRows))
	require.NoError(t, store.db.QueryRow(`SELECT COUNT(*) FROM content_fts_rowid`).Scan(&ownerRows))
	require.NoError(t, store.db.QueryRow(`
SELECT COUNT(*)
FROM content_fts AS f
JOIN content_fts_rowid AS o
  ON o.fts_rowid = f.rowid
 AND o.repo_prefix = f.repo_prefix
 AND o.file_path = f.file_path`).Scan(&exactRows))
	require.Equal(t, ftsRows, ownerRows)
	require.Equal(t, ftsRows, exactRows)
}

func TestReplaceContentFilesUsesBoundedStatementsAndStableDedup(t *testing.T) {
	store := openReindexReceiptTestStore(t)
	files := make([]graph.ContentFTSFileReplacement, 0, 71)
	for i := 0; i < 70; i++ {
		files = append(files, contentReplacement(fmt.Sprintf("docs/%03d.md", i), "initialbatchtoken", 2))
	}
	// Duplicate files are last-write-wins without creating a third group;
	// duplicate node IDs within that winner retain their first position and
	// take their last payload.
	winner := contentReplacement("docs/000.md", "discardedwinner", 1)
	winner.Items = append(winner.Items, graph.ContentFTSItem{
		NodeID: winner.Items[0].NodeID, Body: "dedupwinner", Ordinal: 99,
	})
	files = append(files, winner)

	stats, err := store.replaceContentFiles(files, "repoA")
	require.NoError(t, err)
	require.Equal(t, contentFTSReplaceStats{
		allocatorQueries:          2,
		ftsDeleteStatements:       2,
		ownershipDeleteStatements: 2,
		insertStatements:          2,
		ownershipInsertStatements: 2,
		commits:                   2,
	}, stats)

	var rows, winners, wrongPaths int
	require.NoError(t, store.db.QueryRow(`SELECT COUNT(*) FROM content_fts`).Scan(&rows))
	require.NoError(t, store.db.QueryRow(`SELECT COUNT(*) FROM content_fts WHERE content_fts MATCH 'dedupwinner'`).Scan(&winners))
	require.NoError(t, store.db.QueryRow(`SELECT COUNT(*) FROM content_fts WHERE file_path = 'caller-path-must-not-win'`).Scan(&wrongPaths))
	require.Equal(t, 139, rows)
	require.Equal(t, 1, winners)
	require.Zero(t, wrongPaths)
	assertContentFTSIntegrity(t, store)
}

func TestReplaceContentFilesScopesSameNamedFilesAndEmptyReplacement(t *testing.T) {
	store := openReindexReceiptTestStore(t)
	require.NoError(t, store.ReplaceContentFiles("repoA", []graph.ContentFTSFileReplacement{
		contentReplacement("shared.md", "alphaold", 2),
	}))
	require.NoError(t, store.ReplaceContentFiles("repoB", []graph.ContentFTSFileReplacement{
		contentReplacement("shared.md", "betasibling", 1),
	}))
	require.NoError(t, store.ReplaceContentFiles("repoA", []graph.ContentFTSFileReplacement{
		contentReplacement("shared.md", "alphanew", 1),
	}))

	alpha, err := store.SearchContent("alphanew", "repoA", 10)
	require.NoError(t, err)
	require.Len(t, alpha, 1)
	beta, err := store.SearchContent("betasibling", "repoB", 10)
	require.NoError(t, err)
	require.Len(t, beta, 1, "repoA replacement must preserve repoB's same-named file")

	require.NoError(t, store.ReplaceContentFiles("repoA", []graph.ContentFTSFileReplacement{{
		FilePath: "shared.md",
	}}))
	alpha, err = store.SearchContent("alphanew", "repoA", 10)
	require.NoError(t, err)
	require.Empty(t, alpha, "empty replacement is authoritative")
	beta, err = store.SearchContent("betasibling", "repoB", 10)
	require.NoError(t, err)
	require.Len(t, beta, 1)
	assertContentFTSIntegrity(t, store)
}

func TestReplaceContentFilesRollsBackDeleteAndInsertTogether(t *testing.T) {
	store := openReindexReceiptTestStore(t)
	require.NoError(t, store.ReplaceContentFiles("repoA", []graph.ContentFTSFileReplacement{
		contentReplacement("target.md", "oldtarget", 1),
	}))
	require.NoError(t, store.ReplaceContentFiles("repoB", []graph.ContentFTSFileReplacement{
		contentReplacement("keeper.md", "keeper", 1),
	}))

	// Reserve the next ownership rowid without an FTS row. Replacement first
	// deletes target.md and inserts its new FTS row, then this primary-key
	// collision fails the ownership insert. The enclosing transaction must
	// restore the old target row and discard the new FTS row.
	var nextRowid int64
	require.NoError(t, store.db.QueryRow(`SELECT MAX(rowid) + 1 FROM content_fts`).Scan(&nextRowid))
	_, err := store.writerDB.Exec(`INSERT INTO content_fts_rowid (fts_rowid, repo_prefix, file_path) VALUES (?, 'fault', 'fault.md')`, nextRowid)
	require.NoError(t, err)

	err = store.ReplaceContentFiles("repoA", []graph.ContentFTSFileReplacement{
		contentReplacement("target.md", "newtarget", 1),
	})
	require.Error(t, err)
	oldHits, searchErr := store.SearchContent("oldtarget", "repoA", 10)
	require.NoError(t, searchErr)
	require.Len(t, oldHits, 1)
	newHits, searchErr := store.SearchContent("newtarget", "repoA", 10)
	require.NoError(t, searchErr)
	require.Empty(t, newHits)

	_, err = store.writerDB.Exec(`DELETE FROM content_fts_rowid WHERE fts_rowid = ?`, nextRowid)
	require.NoError(t, err)
	assertContentFTSIntegrity(t, store)
}
