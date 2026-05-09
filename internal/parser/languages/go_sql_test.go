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

func TestGoSQL_EmitsColumnReadsAndWrites(t *testing.T) {
	src := `package foo

type DB struct{}
func (d *DB) Query(q string, args ...any) (any, error) { return nil, nil }
func (d *DB) Exec(q string, args ...any) (any, error)  { return nil, nil }

func Run(db *DB) {
	_, _ = db.Query("SELECT id, email FROM users WHERE id = $1")
	_, _ = db.Exec("UPDATE users SET email = $1, updated_at = NOW() WHERE id = $2")
	_, _ = db.Exec("INSERT INTO sessions (user_id, token) VALUES ($1, $2)")
}
`
	fix := runGoExtract(t, src)

	cols := fix.nodesByKind[graph.KindColumn]
	if len(cols) == 0 {
		t.Fatalf("expected KindColumn nodes; got none")
	}
	wantCols := map[string]bool{
		"col::generic::users.id":          false,
		"col::generic::users.email":       false,
		"col::generic::users.updated_at":  false,
		"col::generic::sessions.user_id":  false,
		"col::generic::sessions.token":    false,
	}
	for _, n := range cols {
		if _, ok := wantCols[n.ID]; ok {
			wantCols[n.ID] = true
		}
	}
	for id, found := range wantCols {
		if !found {
			t.Errorf("expected KindColumn %s; got %v", id, nodeIDs(cols))
		}
	}

	reads := fix.edgesByKind[graph.EdgeReadsCol]
	hasIDRead := false
	for _, e := range reads {
		if e.To == "col::generic::users.id" {
			hasIDRead = true
		}
	}
	if !hasIDRead {
		t.Errorf("expected EdgeReadsCol → users.id; got %v", edgeTargets(reads))
	}

	writes := fix.edgesByKind[graph.EdgeWritesCol]
	hasEmailWrite := false
	hasTokenWrite := false
	for _, e := range writes {
		if e.To == "col::generic::users.email" {
			hasEmailWrite = true
		}
		if e.To == "col::generic::sessions.token" {
			hasTokenWrite = true
		}
	}
	if !hasEmailWrite {
		t.Errorf("expected EdgeWritesCol → users.email; got %v", edgeTargets(writes))
	}
	if !hasTokenWrite {
		t.Errorf("expected EdgeWritesCol → sessions.token; got %v", edgeTargets(writes))
	}
}

func nodeIDs(nodes []*graph.Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.ID)
	}
	return out
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

func TestGoSQL_DialectInferredFromPostgresImport(t *testing.T) {
	src := `package foo

import (
	_ "github.com/lib/pq"
	"database/sql"
)

func Run(db *sql.DB) {
	_, _ = db.Query("SELECT * FROM users")
}
`
	fix := runGoExtract(t, src)
	tables := fix.nodesByKind[graph.KindTable]
	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
	}
	if tables[0].ID != "db::postgres::users" {
		t.Errorf("expected postgres dialect on ID, got %q", tables[0].ID)
	}
	if d, _ := tables[0].Meta["dialect"].(string); d != "postgres" {
		t.Errorf("dialect meta = %q", d)
	}
}

func TestGoSQL_DialectInferredFromMysqlImport(t *testing.T) {
	src := `package foo

import (
	_ "github.com/go-sql-driver/mysql"
	"database/sql"
)

func Run(db *sql.DB) {
	_, _ = db.Query("SELECT * FROM orders")
}
`
	fix := runGoExtract(t, src)
	tables := fix.nodesByKind[graph.KindTable]
	if len(tables) != 1 || tables[0].ID != "db::mysql::orders" {
		t.Errorf("expected mysql dialect, got %+v", tables)
	}
}

func TestGoSQL_DialectInferredFromPgxV5(t *testing.T) {
	// Major-version-suffix imports (pgx/v5) resolve via the
	// prefix-stripped lookup.
	src := `package foo

import (
	"github.com/jackc/pgx/v5"
)

type Pool struct{}
func (p *Pool) Query(q string) (any, error) { return nil, nil }

func Run(p *Pool) {
	_, _ = p.Query("SELECT * FROM users")
}
`
	fix := runGoExtract(t, src)
	tables := fix.nodesByKind[graph.KindTable]
	if len(tables) != 1 || tables[0].ID != "db::postgres::users" {
		t.Errorf("expected postgres (pgx/v5 prefix-stripped to pgx), got %+v", tables)
	}
}

func TestGoSQL_NoDriverFallsBackToGeneric(t *testing.T) {
	src := `package foo

type DB struct{}
func (d *DB) Query(q string) (any, error) { return nil, nil }

func Run(db *DB) {
	_, _ = db.Query("SELECT * FROM users")
}
`
	fix := runGoExtract(t, src)
	tables := fix.nodesByKind[graph.KindTable]
	if len(tables) != 1 || tables[0].ID != "db::generic::users" {
		t.Errorf("expected generic dialect fallback, got %+v", tables)
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
