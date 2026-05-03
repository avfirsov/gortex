package sql

import (
	"testing"
)

func TestExtractTables_BasicSelect(t *testing.T) {
	refs := ExtractTables(`SELECT * FROM users WHERE id = 1`)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d: %+v", len(refs), refs)
	}
	if refs[0].Table != "users" || refs[0].Op != "select" {
		t.Errorf("got %+v", refs[0])
	}
}

func TestExtractTables_JoinClauses(t *testing.T) {
	refs := ExtractTables(`SELECT u.name FROM users u JOIN orders o ON u.id = o.user_id`)
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d: %+v", len(refs), refs)
	}
	got := map[string]bool{}
	for _, r := range refs {
		got[r.Table] = true
	}
	if !got["users"] || !got["orders"] {
		t.Errorf("missing refs: %v", got)
	}
}

func TestExtractTables_InsertUpdateDelete(t *testing.T) {
	cases := []struct {
		query string
		op    string
		table string
	}{
		{`INSERT INTO users (id, name) VALUES (1, 'a')`, "insert", "users"},
		{`UPDATE accounts SET balance = 0 WHERE id = 1`, "update", "accounts"},
		{`DELETE FROM sessions WHERE expired = true`, "delete", "sessions"},
		{`TRUNCATE TABLE logs`, "truncate", "logs"},
		{`TRUNCATE logs2`, "truncate", "logs2"},
	}
	for _, c := range cases {
		refs := ExtractTables(c.query)
		if len(refs) != 1 {
			t.Fatalf("%q → %d refs, want 1", c.query, len(refs))
		}
		if refs[0].Op != c.op || refs[0].Table != c.table {
			t.Errorf("%q → %+v, want op=%q table=%q", c.query, refs[0], c.op, c.table)
		}
	}
}

func TestExtractTables_QuotedIdentifiers(t *testing.T) {
	cases := []string{
		`SELECT * FROM "users" WHERE id = 1`,    // ANSI
		"SELECT * FROM `users` WHERE id = 1",    // MySQL backticks
		`SELECT * FROM [users] WHERE id = 1`,    // T-SQL brackets
	}
	for _, q := range cases {
		refs := ExtractTables(q)
		if len(refs) != 1 || refs[0].Table != "users" {
			t.Errorf("%q → %+v, want table=users", q, refs)
		}
	}
}

func TestExtractTables_SchemaQualified(t *testing.T) {
	refs := ExtractTables(`SELECT * FROM public.users JOIN auth.sessions ON id = session_id`)
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}
	got := map[string]string{} // table → schema
	for _, r := range refs {
		got[r.Table] = r.Schema
	}
	if got["users"] != "public" {
		t.Errorf("users schema = %q, want public", got["users"])
	}
	if got["sessions"] != "auth" {
		t.Errorf("sessions schema = %q, want auth", got["sessions"])
	}
}

func TestExtractTables_DeduplicatesSameOpAndTable(t *testing.T) {
	refs := ExtractTables(`SELECT * FROM users JOIN users u2 ON 1=1`)
	// Both users references share op=select, schema="" — should dedupe.
	if len(refs) != 1 {
		t.Errorf("expected dedup to single users ref, got %d", len(refs))
	}
}

func TestExtractTables_MixedOps(t *testing.T) {
	q := `WITH x AS (SELECT * FROM source)
INSERT INTO target SELECT * FROM x`
	refs := ExtractTables(q)
	// `source` (select) + `target` (insert) + `x` (select) = 3
	if len(refs) != 3 {
		t.Errorf("expected 3 refs, got %d: %+v", len(refs), refs)
	}
}

func TestExtractTables_Empty(t *testing.T) {
	if r := ExtractTables(""); len(r) != 0 {
		t.Errorf("empty query should yield no refs")
	}
	if r := ExtractTables("SELECT 1"); len(r) != 0 {
		t.Errorf("no-table query should yield no refs")
	}
}

func TestStripQuoting(t *testing.T) {
	cases := []struct{ in, want string }{
		{`"users"`, "users"},
		{"`users`", "users"},
		{"[users]", "users"},
		{"users", "users"},
		{`"`, `"`},
	}
	for _, c := range cases {
		if got := stripQuoting(c.in); got != c.want {
			t.Errorf("stripQuoting(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSplitSchemaTable(t *testing.T) {
	cases := []struct {
		in           string
		schema, table string
	}{
		{"users", "", "users"},
		{"public.users", "public", "users"},
		{"db.public.users", "public", "users"}, // database segment dropped
		{`"public"."users"`, "public", "users"},
	}
	for _, c := range cases {
		s, t2 := splitSchemaTable(c.in)
		if s != c.schema || t2 != c.table {
			t.Errorf("splitSchemaTable(%q) = (%q,%q), want (%q,%q)", c.in, s, t2, c.schema, c.table)
		}
	}
}

func TestTableNodeID(t *testing.T) {
	cases := []struct {
		dialect, schema, table, want string
	}{
		{"postgres", "public", "users", "db::postgres::public.users"},
		{"", "", "users", "db::generic::users"},
		{"mysql", "", "orders", "db::mysql::orders"},
	}
	for _, c := range cases {
		if got := TableNodeID(c.dialect, c.schema, c.table); got != c.want {
			t.Errorf("TableNodeID(%q,%q,%q) = %q, want %q",
				c.dialect, c.schema, c.table, got, c.want)
		}
	}
}
