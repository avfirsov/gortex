package store_sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

// hasNodeColumn reports whether the nodes table currently has the named column.
func hasNodeColumn(t *testing.T, db *sql.DB, col string) bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(nodes)`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var (
			cid, notnull, pk int
			name, ctype      string
			dflt             sql.NullString
		)
		require.NoError(t, rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk))
		if name == col {
			return true
		}
	}
	require.NoError(t, rows.Err())
	return false
}

// TestOpenUpgradesPreDataClassStore is the backward-compatibility proof for the
// promoted data_class column: under the historical v2 plan, an existing v1
// store written before the column existed must open cleanly (ensureNodeColumns ALTERs the column in before the
// node statements are prepared), keep its rows, and immediately get the working
// SQL-level content filter — all WITHOUT a schema_version bump or a reindex.
//
// The simulated old store has data_class dropped while staying at the v1
// baseline, the exact shape of every Gortex sqlite DB already on disk before
// this change. Without the data_class entry in promotedMetaColumns, the reopen
// fails with "no such column: data_class" when prepare() builds the node
// INSERT/SELECT — so this test fails-closed if that wiring regresses.
func TestOpenUpgradesPreDataClassStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.sqlite")

	// 1. Create a current store and seed a row, then close. A fresh store is
	//    stamped at the current schema version; step 2 knocks it back to the v1
	//    baseline to simulate a store written before data_class existed.
	s, err := Open(path)
	require.NoError(t, err)
	s.AddNode(&graph.Node{ID: "old1", Kind: graph.KindFunction, Name: "Legacy", FilePath: "f.go", RepoPrefix: "r"})
	require.NoError(t, s.Close())

	// 2. Simulate a store written before data_class existed: drop the column
	//    while leaving user_version at the v1 baseline.
	withRawDB(t, path, func(db *sql.DB) {
		_, err := db.Exec(`ALTER TABLE nodes DROP COLUMN data_class`)
		require.NoError(t, err, "simulate a pre-data_class store")
		require.False(t, hasNodeColumn(t, db, "data_class"), "data_class must be absent before the upgrade")

		// A fresh Open stamps the current schema version; knock it back to the v1
		// baseline so this genuinely simulates a pre-data_class store and the
		// reopen exercises the in-place upgrade arm rather than a no-op.
		_, err = db.Exec(`PRAGMA user_version = 1`)
		require.NoError(t, err, "reset to the v1 baseline")

		var v int
		require.NoError(t, db.QueryRow(`PRAGMA user_version`).Scan(&v))
		require.Equal(t, 1, v, "the simulated old store must sit at the v1 baseline")
	})

	// 3. Reopen under the historical v2 plan. ensureNodeColumns must re-add the
	//    column before prepare() references it, so open succeeds without a wipe.
	//    Shipped v3 deliberately rebuilds every older topology cache.
	s2, err := openWith(path, 2, schemaMigrations[:1], false)
	require.NoError(t, err, "Open must upgrade a pre-data_class store in place, not fail on the missing column")
	t.Cleanup(func() { _ = s2.Close() })

	require.True(t, hasNodeColumn(t, s2.db, "data_class"), "ensureNodeColumns must re-add data_class on Open")
	require.False(t, s2.NeedsRebuild(), "an additive-column upgrade must not signal a wipe/reindex")

	// 4. Existing rows survived (the upgrade is in place, not a rebuild).
	require.NotNil(t, s2.GetNode("old1"), "existing rows must survive the in-place upgrade")

	// 5. A new content node persists, round-trips through the promoted column,
	//    and is correctly dropped by the SQL-level content filter.
	s2.AddNode(&graph.Node{ID: "content1", Kind: graph.KindDoc, Name: "doc.txt::0", RepoPrefix: "r",
		Meta: map[string]any{"data_class": "content", "section_text": "x"}})
	s2.AddNode(&graph.Node{ID: "code1", Kind: graph.KindFunction, Name: "Foo", RepoPrefix: "r"})

	content := s2.GetNode("content1")
	require.NotNil(t, content)
	require.Equal(t, "content", content.Meta["data_class"], "data_class round-trips via the promoted column after upgrade")

	ids := map[string]bool{}
	for _, n := range s2.GetRepoNonContentNodes("r") {
		ids[n.ID] = true
	}
	require.True(t, ids["old1"], "legacy non-content node kept")
	require.True(t, ids["code1"], "code node kept")
	require.False(t, ids["content1"], "content node filtered at the SQL level after the upgrade")
	require.Len(t, ids, 2)
}
