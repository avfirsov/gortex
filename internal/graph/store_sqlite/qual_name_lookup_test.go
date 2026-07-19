package store_sqlite

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestGetNodesByQualNamesUsesUniquePartialIndexAndReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "qual-name.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	store.AddBatch([]*graph.Node{
		{ID: "node::zeta", Kind: graph.KindFunction, Name: "Zeta", QualName: "pkg.Zeta", FilePath: "z.go"},
		{ID: "node::alpha", Kind: graph.KindFunction, Name: "Alpha", QualName: "pkg.Alpha", FilePath: "a.go"},
		{ID: "node::middle", Kind: graph.KindFunction, Name: "Middle", QualName: "pkg.Middle", FilePath: "m.go"},
	}, nil)

	assertQualNameLookupPlan(t, store)
	assertQualNameLookupParity(t, store)

	ordered := store.queryNodesSQL(
		nodesByQualNameLookupSQL,
		qualNameLookupPayload([]string{"pkg.Zeta", "pkg.Alpha", "pkg.Middle"}),
	)
	if len(ordered) != 3 {
		t.Fatalf("ordered lookup returned %d nodes, want 3", len(ordered))
	}
	for i, want := range []string{"pkg.Alpha", "pkg.Middle", "pkg.Zeta"} {
		if ordered[i].QualName != want {
			t.Fatalf("ordered lookup[%d] qual_name = %q, want %q", i, ordered[i].QualName, want)
		}
	}

	// Preserve the existing qualified-name quality contract: the partial
	// index is UNIQUE for non-empty values, even though empty qual_names may
	// repeat freely.
	if _, err := store.writerDB.Exec(
		`INSERT INTO nodes(id, kind, name, qual_name, file_path) VALUES (?, ?, ?, ?, ?)`,
		"node::duplicate", graph.KindFunction, "Duplicate", "pkg.Alpha", "duplicate.go",
	); err == nil {
		t.Fatal("duplicate non-empty qual_name unexpectedly bypassed nodes_by_qual uniqueness")
	}
	if got := store.GetNodeByQualName("pkg.Alpha"); got == nil || got.ID != "node::alpha" {
		t.Fatalf("failed duplicate insert changed original qualified-name owner: %#v", got)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	assertQualNameLookupPlan(t, store)
	assertQualNameLookupParity(t, store)
}

func TestGetNodesByQualNamesSingleJSONBindHandles40001Names(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "large-qual-name.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	store.AddBatch([]*graph.Node{
		{ID: "node::first", Kind: graph.KindFunction, Name: "First", QualName: "hit.first", FilePath: "first.go"},
		{ID: "node::middle", Kind: graph.KindFunction, Name: "Middle", QualName: "hit.middle", FilePath: "middle.go"},
		{ID: "node::last", Kind: graph.KindFunction, Name: "Last", QualName: "hit.last", FilePath: "last.go"},
	}, nil)

	const count = 40001
	qualNames := make([]string, count)
	for i := range qualNames {
		qualNames[i] = fmt.Sprintf("missing.%05d", i)
	}
	qualNames[0] = "hit.first"
	qualNames[count/2] = "hit.middle"
	qualNames[count-1] = "hit.last"

	if binds := strings.Count(nodesByQualNameLookupSQL, "?"); binds != 1 {
		t.Fatalf("qualified-name lookup bind count = %d, want exactly 1", binds)
	}
	got := store.GetNodesByQualNames(qualNames)
	if len(got) != 3 {
		t.Fatalf("large qualified-name lookup returned %d matches, want 3", len(got))
	}
	for qualName, wantID := range map[string]string{
		"hit.first":  "node::first",
		"hit.middle": "node::middle",
		"hit.last":   "node::last",
	} {
		if node := got[qualName]; node == nil || node.ID != wantID {
			t.Fatalf("large lookup[%q] = %#v, want id %q", qualName, node, wantID)
		}
	}
}

func TestGetNodesByQualNamesFailsClosedOnDecodeQueryAndClosedStoreErrors(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "qual-name-errors.sqlite"))
	if err != nil {
		t.Fatal(err)
	}

	store.AddBatch([]*graph.Node{
		{ID: "node::bad", Kind: graph.KindFunction, Name: "Bad", QualName: "bad.qual", FilePath: "bad.go"},
		{ID: "node::good", Kind: graph.KindFunction, Name: "Good", QualName: "good.qual", FilePath: "good.go"},
	}, nil)
	if _, err := store.writerDB.Exec(`UPDATE nodes SET start_line = 'not-an-integer' WHERE id = 'node::bad'`); err != nil {
		t.Fatal(err)
	}

	got := store.GetNodesByQualNames([]string{"bad.qual", "good.qual"})
	if len(got) != 1 || got["good.qual"] == nil || got["good.qual"].ID != "node::good" {
		t.Fatalf("decode failure should skip only the corrupt row, got %#v", got)
	}
	if got["bad.qual"] != nil {
		t.Fatalf("corrupt row unexpectedly survived strict node decoding: %#v", got["bad.qual"])
	}

	// INDEXED BY is deliberate: losing the intended index must fail closed
	// instead of silently degrading into a full nodes-table scan.
	if _, err := store.writerDB.Exec(`DROP INDEX nodes_by_qual`); err != nil {
		t.Fatal(err)
	}
	if got := store.GetNodesByQualNames([]string{"good.qual"}); len(got) != 0 {
		t.Fatalf("lookup without mandatory index returned %#v, want empty", got)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if got := store.GetNodesByQualNames([]string{"good.qual"}); len(got) != 0 {
		t.Fatalf("lookup on closed store returned %#v, want empty", got)
	}
}

func assertQualNameLookupPlan(t *testing.T, store *Store) {
	t.Helper()
	rows, err := store.writerDB.Query(
		`EXPLAIN QUERY PLAN `+nodesByQualNameLookupSQL,
		qualNameLookupPayload([]string{"pkg.Alpha", "pkg.Zeta"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	plan := strings.Join(details, " | ")
	if !strings.Contains(plan, "SEARCH nodes USING INDEX nodes_by_qual") {
		t.Fatalf("qualified-name query did not seek through nodes_by_qual: %s", plan)
	}
	if strings.Contains(plan, "SCAN nodes") {
		t.Fatalf("qualified-name query regressed to a full nodes scan: %s", plan)
	}
}

func assertQualNameLookupParity(t *testing.T, store *Store) {
	t.Helper()
	input := []string{"pkg.Zeta", "", "missing.qual", "pkg.Alpha", "pkg.Zeta", "pkg.Middle"}
	got := store.GetNodesByQualNames(input)
	if len(got) != 3 {
		t.Fatalf("batched qualified-name lookup returned %d matches, want 3", len(got))
	}
	for _, qualName := range []string{"pkg.Alpha", "pkg.Middle", "pkg.Zeta"} {
		want := store.GetNodeByQualName(qualName)
		if want == nil {
			t.Fatalf("individual lookup unexpectedly missed %q", qualName)
		}
		if node := got[qualName]; node == nil || node.ID != want.ID {
			t.Fatalf("batch lookup[%q] = %#v, individual lookup = %#v", qualName, node, want)
		}
	}
	if _, exists := got["missing.qual"]; exists {
		t.Fatal("batched lookup invented a missing qualified name")
	}
	if _, exists := got[""]; exists {
		t.Fatal("batched lookup retained an empty qualified name")
	}
}
