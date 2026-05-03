package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestGoSQL_DetectsTablesFromQuery(t *testing.T) {
	src := `package foo

type DB struct{}

func (d *DB) Query(q string, args ...any) (any, error) { return nil, nil }
func (d *DB) Exec(q string, args ...any) (any, error)  { return nil, nil }

func Run(db *DB) {
	_, _ = db.Query("SELECT id, name FROM users WHERE active = true")
	_, _ = db.Exec("INSERT INTO sessions (user_id) VALUES (1)")
}
`
	fix := runGoExtract(t, src)

	tables := fix.nodesByKind[graph.KindTable]
	if len(tables) != 2 {
		t.Fatalf("expected 2 KindTable, got %d: %+v", len(tables), tables)
	}
	gotIDs := map[string]bool{}
	for _, n := range tables {
		gotIDs[n.ID] = true
	}
	if !gotIDs["db::generic::users"] {
		t.Errorf("missing users table, got %v", gotIDs)
	}
	if !gotIDs["db::generic::sessions"] {
		t.Errorf("missing sessions table, got %v", gotIDs)
	}

	queries := fix.edgesByKind[graph.EdgeQueries]
	if len(queries) != 2 {
		t.Errorf("expected 2 EdgeQueries, got %d", len(queries))
	}
}

func TestGoSQL_DynamicQuerySkipped(t *testing.T) {
	src := `package foo

type DB struct{}

func (d *DB) Query(q string) (any, error) { return nil, nil }

func Run(db *DB, q string) {
	_, _ = db.Query(q)
}
`
	fix := runGoExtract(t, src)
	if got := len(fix.nodesByKind[graph.KindTable]); got != 0 {
		t.Errorf("dynamic query should not produce tables, got %d", got)
	}
}

func TestGoSQL_NonSQLMethodIgnored(t *testing.T) {
	src := `package foo

type Cache struct{}

func (c *Cache) Set(key string, val any) {}

func Run(c *Cache) {
	c.Set("FROM users", nil)
}
`
	fix := runGoExtract(t, src)
	if got := len(fix.nodesByKind[graph.KindTable]); got != 0 {
		t.Errorf("non-SQL method must not produce tables, got %d", got)
	}
}

func TestGoSQL_SchemaQualifiedTables(t *testing.T) {
	src := `package foo

type DB struct{}

func (d *DB) Query(q string) (any, error) { return nil, nil }

func Run(db *DB) {
	_, _ = db.Query("SELECT * FROM public.users JOIN auth.sessions ON id = session_id")
}
`
	fix := runGoExtract(t, src)
	tables := fix.nodesByKind[graph.KindTable]
	if len(tables) != 2 {
		t.Fatalf("expected 2 schema-qualified tables, got %d", len(tables))
	}
	gotSchemas := map[string]string{}
	for _, n := range tables {
		schema, _ := n.Meta["schema"].(string)
		table, _ := n.Meta["table"].(string)
		gotSchemas[table] = schema
	}
	if gotSchemas["users"] != "public" {
		t.Errorf("users schema = %q", gotSchemas["users"])
	}
	if gotSchemas["sessions"] != "auth" {
		t.Errorf("sessions schema = %q", gotSchemas["sessions"])
	}
}

func TestGoSQL_DedupedAcrossCallSites(t *testing.T) {
	src := `package foo

type DB struct{}

func (d *DB) Query(q string) (any, error) { return nil, nil }

func A(db *DB) { _, _ = db.Query("SELECT * FROM users") }
func B(db *DB) { _, _ = db.Query("SELECT id FROM users WHERE id = 1") }
`
	fix := runGoExtract(t, src)
	tables := fix.nodesByKind[graph.KindTable]
	if len(tables) != 1 {
		t.Errorf("expected 1 deduped users node, got %d", len(tables))
	}
	if got := len(fix.edgesByKind[graph.EdgeQueries]); got != 2 {
		t.Errorf("expected 2 EdgeQueries (one per caller), got %d", got)
	}
}
