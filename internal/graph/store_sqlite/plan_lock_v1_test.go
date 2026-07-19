package store_sqlite

import (
	"strings"
	"testing"
)

// Plan locks for the shapes the production-scale SQL sweep found misplanning.
// Each query text mirrors its production site (kept in sync by hand — the
// access path lives in the FROM/WHERE text, and drifting a production query
// without updating its lock is exactly the failure these tests exist to
// catch). Properties, not index names, wherever two indexes would be equally
// correct.
func TestSweepPlanLocks(t *testing.T) {
	s := newPlanLockFixture(t)

	cases := []struct {
		name   string
		query  string
		args   int
		want   []string
		forbid []string
	}{
		{
			// store.go stmtGetNodeByQual: the literal qual_name <> ''
			// conjunct is what admits the partial nodes_by_qual index; a
			// bound parameter alone cannot be proven non-empty and the
			// statement scans all nodes (measured on a production store).
			name:   "get_node_by_qual",
			query:  `SELECT ` + lookupNodeCols + ` FROM nodes WHERE qual_name = ? AND qual_name <> '' LIMIT 1`,
			args:   1,
			want:   []string{"nodes_by_qual (qual_name=?)"},
			forbid: []string{"SCAN nodes"},
		},
		{
			// edgeExactDeleteByIdentitySQL: the IN-over-JOIN shape drives
			// from the VALUES list into the edges primary key. The prior
			// correlated EXISTS scanned all edges per chunk.
			name:  "edge_exact_identity_delete",
			query: edgeExactDeleteByIdentitySQL("(?,?,?,?,?),(?,?,?,?,?),(?,?,?,?,?)"),
			args:  15,
			want: []string{
				"SEARCH e USING COVERING INDEX sqlite_autoindex_edges_1 (from_id=? AND to_id=? AND kind=? AND file_path=? AND line=?)",
			},
			forbid: []string{"SCAN edges", "SCAN e"},
		},
		{
			// lsp_projection ProjectFileNodes: requested_files must drive
			// nodes_by_file; the repo predicate is demoted so the planner
			// cannot read the whole repo per changed-file batch.
			name: "lsp_project_file_nodes",
			query: `
WITH requested_languages(language) AS (
    SELECT CAST(value AS TEXT) FROM json_each(?)
), requested_files(file_path) AS (
    SELECT CAST(value AS TEXT) FROM json_each(?)
)
SELECT ` + qualifiedNodeColumns("n", lookupNodeColsLight) + `
FROM requested_files AS f
CROSS JOIN nodes AS n
JOIN requested_languages AS l ON l.language = n.language
WHERE n.file_path = f.file_path
  AND +n.repo_prefix = ?
  AND n.kind NOT IN (?, ?)
ORDER BY n.file_path, n.id`,
			args:   5,
			want:   []string{"SEARCH n USING INDEX nodes_by_file (file_path=?)"},
			forbid: []string{"nodes_by_repo", "SCAN n"},
		},
		{
			// lsp_projection ProjectFileEdges: files drive nodes, nodes
			// drive edges_by_from — never a whole-repo read.
			name: "lsp_project_file_edges",
			query: `
WITH requested_languages(language) AS (
    SELECT CAST(value AS TEXT) FROM json_each(?)
), requested_files(file_path) AS (
    SELECT CAST(value AS TEXT) FROM json_each(?)
)
SELECT ` + lookupQualifiedEdgeCols + `
FROM requested_files AS f
CROSS JOIN nodes AS n
JOIN requested_languages AS l ON l.language = n.language
CROSS JOIN edges AS e
WHERE n.file_path = f.file_path
  AND e.from_id = n.id
  AND +n.repo_prefix = ?
  AND e.kind NOT IN (?, ?, ?, ?, ?, ?)
ORDER BY e.from_id, e.to_id, e.kind, e.file_path, e.line`,
			args: 9,
			want: []string{
				"SEARCH n USING INDEX nodes_by_file (file_path=?)",
				"SEARCH e USING INDEX edges_by_from", "(from_id=?",
			},
			forbid: []string{"nodes_by_repo", "SCAN n", "SCAN e"},
		},
		{
			name: "lsp_project_file_edges_by_kinds",
			query: `
WITH requested_languages(language) AS (
    SELECT CAST(value AS TEXT) FROM json_each(?)
), requested_files(file_path) AS (
    SELECT CAST(value AS TEXT) FROM json_each(?)
), requested_kinds(kind) AS (
    SELECT CAST(value AS TEXT) FROM json_each(?)
)
SELECT ` + lookupQualifiedEdgeCols + `
FROM requested_files AS f
CROSS JOIN nodes AS n
JOIN requested_languages AS l ON l.language = n.language
CROSS JOIN edges AS e
JOIN requested_kinds AS k ON k.kind = e.kind
WHERE n.file_path = f.file_path
  AND e.from_id = n.id
  AND +n.repo_prefix = ?
ORDER BY e.from_id, e.to_id, e.kind, e.file_path, e.line`,
			args: 4,
			want: []string{
				"SEARCH n USING INDEX nodes_by_file (file_path=?)",
				"SEARCH e USING INDEX edges_by_from", "(from_id=?",
			},
			forbid: []string{"nodes_by_repo", "SCAN n", "SCAN e"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := explainQueryPlan(t, s, tc.query, tc.args)
			joined := strings.Join(plan, "\n")
			for _, want := range tc.want {
				if !strings.Contains(joined, want) {
					t.Fatalf("plan missing %q:\n%s", want, joined)
				}
			}
			for _, forbid := range tc.forbid {
				if strings.Contains(joined, forbid) {
					t.Fatalf("plan contains forbidden %q:\n%s", forbid, joined)
				}
			}
		})
	}
}

// The receiver-rebind batch drives from the TEMP file table (CROSS JOIN pin);
// the temp table is connection-local, so this lock provisions it through the
// same writer path production uses.
func TestSweepPlanLockReceiverRebindBatch(t *testing.T) {
	s := newPlanLockFixture(t)
	conn, err := s.writerDB.Conn(t.Context())
	if err != nil {
		t.Fatalf("writer conn: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(t.Context(), goMethodReceiverFileTableSQL); err != nil {
		t.Fatalf("create temp table: %v", err)
	}
	rows, err := conn.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+goMethodReceiverCandidatesForFilesSQL)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan: %v", err)
		}
		plan = append(plan, detail)
	}
	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "SCAN f") && !strings.Contains(joined, "go_receiver_rebind_files") {
		t.Fatalf("plan must drive from the temp file table:\n%s", joined)
	}
	if !strings.Contains(joined, "nodes_by_file (file_path=?)") {
		t.Fatalf("plan must probe nodes_by_file per requested file:\n%s", joined)
	}
}
