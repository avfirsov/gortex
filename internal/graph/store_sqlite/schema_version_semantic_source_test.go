package store_sqlite

import (
	"bytes"
	"database/sql"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func legacyGobMeta(t *testing.T, meta map[string]any) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(meta); err != nil {
		t.Fatalf("encode legacy gob Meta: %v", err)
	}
	return buf.Bytes()
}

func seedRepeatedMigrationEdges(t *testing.T, db *sql.DB, prefix string, count int, meta []byte) {
	t.Helper()
	if count <= 0 {
		return
	}
	_, err := db.Exec(`
WITH RECURSIVE seq(n) AS (
    SELECT 1
    UNION ALL
    SELECT n + 1 FROM seq WHERE n < ?
)
INSERT INTO edges (from_id, to_id, kind, file_path, line, meta)
SELECT ? || '-from-' || n, ? || '-to-' || n, 'calls', ? || '.go', n, ?
FROM seq`, count, prefix, prefix, prefix, meta)
	if err != nil {
		t.Fatalf("seed %d %s migration edges: %v", count, prefix, err)
	}
}

func insertMigrationEdge(t *testing.T, db *sql.DB, name string, meta []byte) int64 {
	t.Helper()
	result, err := db.Exec(`
INSERT INTO edges (from_id, to_id, kind, file_path, line, meta)
VALUES (?, ?, 'calls', 'candidate.go', 1, ?)`, name+"-from", name+"-to", meta)
	if err != nil {
		t.Fatalf("seed migration edge %q: %v", name, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("migration edge %q id: %v", name, err)
	}
	return id
}

func TestBackfillEdgeSemanticSourcesPrefiltersLargeCorpus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	noiseMeta, err := encodeMeta(map[string]any{"noise": "ordinary metadata"})
	if err != nil {
		t.Fatalf("encode noise Meta: %v", err)
	}
	const noiseRows = 50_000
	seedRepeatedMigrationEdges(t, store.writerDB, "noise", noiseRows, noiseMeta)

	flatMeta, err := encodeMeta(map[string]any{
		edgeSemanticSourceMetaMarker: "flat-provider",
		"kept":                       "flat",
	})
	if err != nil {
		t.Fatalf("encode flat Meta: %v", err)
	}
	if !isFlatMeta(flatMeta) {
		t.Fatal("test fixture should exercise the flat binary codec")
	}
	jsonMeta, err := json.Marshal(map[string]any{
		edgeSemanticSourceMetaMarker: "json-provider",
		"kept":                       "json",
	})
	if err != nil {
		t.Fatalf("encode JSON Meta: %v", err)
	}
	gobMeta := legacyGobMeta(t, map[string]any{
		edgeSemanticSourceMetaMarker: "gob-provider",
		"kept":                       "gob",
	})
	valueFalsePositive, err := encodeMeta(map[string]any{
		"note": "a value containing semantic_source is not the promoted key",
	})
	if err != nil {
		t.Fatalf("encode value false positive: %v", err)
	}
	typeFalsePositive, err := encodeMeta(map[string]any{
		edgeSemanticSourceMetaMarker: 42,
		"kept":                       "non-string",
	})
	if err != nil {
		t.Fatalf("encode type false positive: %v", err)
	}

	ids := map[string]int64{
		"flat":     insertMigrationEdge(t, store.writerDB, "flat", flatMeta),
		"json":     insertMigrationEdge(t, store.writerDB, "json", jsonMeta),
		"gob":      insertMigrationEdge(t, store.writerDB, "gob", gobMeta),
		"value-fp": insertMigrationEdge(t, store.writerDB, "value-fp", valueFalsePositive),
		"type-fp":  insertMigrationEdge(t, store.writerDB, "type-fp", typeFalsePositive),
	}

	tx, err := store.writerDB.Begin()
	if err != nil {
		t.Fatalf("begin migration: %v", err)
	}
	stats, err := backfillEdgeSemanticSourcesWithLimits(tx, defaultEdgeSemanticSourceMigrationLimits)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("backfill semantic sources: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit migration: %v", err)
	}

	// The SQLite prefilter scans the 50k-row corpus once and returns only the
	// five marker matches. The authoritative decoder then promotes exactly the
	// three top-level string values across all supported codecs.
	if stats.PageQueries != 1 || stats.RowsDecoded != 5 || stats.RowsUpdated != 3 {
		t.Fatalf("migration stats = %+v, want 1 query / 5 decodes / 3 updates", stats)
	}
	if stats.UpdateStatements != 1 {
		t.Fatalf("update statements = %d, want 1", stats.UpdateStatements)
	}
	if stats.MaxPageRows > edgeSemanticSourceMigrationPageRows ||
		stats.MaxPageBytes > edgeSemanticSourceMigrationPageBytes {
		t.Fatalf("production page bounds exceeded: %+v", stats)
	}

	var untouched int
	if err := store.db.QueryRow(`
SELECT COUNT(*) FROM edges
WHERE from_id LIKE 'noise-from-%' AND semantic_source IS NULL AND meta = ?`, noiseMeta).Scan(&untouched); err != nil {
		t.Fatalf("count untouched noise rows: %v", err)
	}
	if untouched != noiseRows {
		t.Fatalf("untouched noise rows = %d, want %d", untouched, noiseRows)
	}

	want := map[string]struct {
		source    string
		remaining map[string]any
	}{
		"flat": {source: "flat-provider", remaining: map[string]any{"kept": "flat"}},
		"json": {source: "json-provider", remaining: map[string]any{"kept": "json"}},
		"gob":  {source: "gob-provider", remaining: map[string]any{"kept": "gob"}},
	}
	for name, expected := range want {
		var source sql.NullString
		var blob []byte
		if err := store.db.QueryRow(`SELECT semantic_source, meta FROM edges WHERE id = ?`, ids[name]).Scan(&source, &blob); err != nil {
			t.Fatalf("read migrated %s edge: %v", name, err)
		}
		if !source.Valid || source.String != expected.source {
			t.Errorf("%s semantic_source = %#v, want %q", name, source, expected.source)
		}
		remaining, err := decodeMeta(blob)
		if err != nil {
			t.Fatalf("decode migrated %s Meta: %v", name, err)
		}
		if !reflect.DeepEqual(remaining, expected.remaining) {
			t.Errorf("%s remaining Meta = %#v, want %#v", name, remaining, expected.remaining)
		}
	}

	for _, name := range []string{"value-fp", "type-fp"} {
		var source sql.NullString
		if err := store.db.QueryRow(`SELECT semantic_source FROM edges WHERE id = ?`, ids[name]).Scan(&source); err != nil {
			t.Fatalf("read %s semantic_source: %v", name, err)
		}
		if source.Valid {
			t.Errorf("%s false positive was promoted to %q", name, source.String)
		}
	}
}

func TestBackfillEdgeSemanticSourcesHonorsPageBounds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		limits  func(blobBytes int64) edgeSemanticSourceMigrationLimits
		rows    int
		queries int
	}{
		{
			name: "row cap",
			limits: func(int64) edgeSemanticSourceMigrationLimits {
				return edgeSemanticSourceMigrationLimits{pageRows: 3, pageBytes: 1 << 20, updateRows: 2}
			},
			rows: 7, queries: 3,
		},
		{
			name: "byte cap",
			limits: func(blobBytes int64) edgeSemanticSourceMigrationLimits {
				return edgeSemanticSourceMigrationLimits{pageRows: 100, pageBytes: blobBytes * 2, updateRows: 100}
			},
			rows: 5, queries: 3,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "store.sqlite")
			store, err := Open(path)
			if err != nil {
				t.Fatalf("open store: %v", err)
			}
			defer store.Close()

			blob, err := encodeMeta(map[string]any{
				edgeSemanticSourceMetaMarker: "provider",
				"padding":                    strings.Repeat("x", 96),
			})
			if err != nil {
				t.Fatalf("encode candidate Meta: %v", err)
			}
			seedRepeatedMigrationEdges(t, store.writerDB, "bounded", tc.rows, blob)
			limits := tc.limits(int64(len(blob)))

			tx, err := store.writerDB.Begin()
			if err != nil {
				t.Fatalf("begin migration: %v", err)
			}
			stats, err := backfillEdgeSemanticSourcesWithLimits(tx, limits)
			if err != nil {
				_ = tx.Rollback()
				t.Fatalf("backfill: %v", err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatalf("commit migration: %v", err)
			}
			if stats.PageQueries != tc.queries || stats.RowsUpdated != tc.rows {
				t.Fatalf("migration stats = %+v, want queries=%d updates=%d", stats, tc.queries, tc.rows)
			}
			if stats.MaxPageRows > limits.pageRows {
				t.Fatalf("max page rows = %d, cap %d", stats.MaxPageRows, limits.pageRows)
			}
			if stats.MaxPageBytes > limits.pageBytes {
				t.Fatalf("max page bytes = %d, cap %d", stats.MaxPageBytes, limits.pageBytes)
			}
		})
	}
}

func TestOpenV4MigratesEdgeSemanticSourceOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("create current store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close current store: %v", err)
	}

	legacy, err := encodeMeta(map[string]any{
		edgeSemanticSourceMetaMarker: "goanalysis",
		"kept":                       "durable",
	})
	if err != nil {
		t.Fatalf("encode legacy Meta: %v", err)
	}
	withRawDB(t, path, func(db *sql.DB) {
		insertMigrationEdge(t, db, "reopen", legacy)
		if _, err := db.Exec(`PRAGMA user_version = 4`); err != nil {
			t.Fatalf("mark store v4: %v", err)
		}
	})

	upgraded, err := Open(path)
	if err != nil {
		t.Fatalf("upgrade v4 store: %v", err)
	}
	var firstSource string
	var firstMeta []byte
	if err := upgraded.db.QueryRow(`SELECT semantic_source, meta FROM edges WHERE from_id = 'reopen-from'`).Scan(&firstSource, &firstMeta); err != nil {
		t.Fatalf("read upgraded edge: %v", err)
	}
	if firstSource != "goanalysis" {
		t.Fatalf("upgraded semantic_source = %q, want goanalysis", firstSource)
	}
	remaining, err := decodeMeta(firstMeta)
	if err != nil {
		t.Fatalf("decode upgraded Meta: %v", err)
	}
	if !reflect.DeepEqual(remaining, map[string]any{"kept": "durable"}) {
		t.Fatalf("upgraded Meta = %#v", remaining)
	}
	if err := upgraded.Close(); err != nil {
		t.Fatalf("close upgraded store: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen v5 store: %v", err)
	}
	defer reopened.Close()
	var secondSource string
	var secondMeta []byte
	if err := reopened.db.QueryRow(`SELECT semantic_source, meta FROM edges WHERE from_id = 'reopen-from'`).Scan(&secondSource, &secondMeta); err != nil {
		t.Fatalf("read reopened edge: %v", err)
	}
	if secondSource != firstSource || !bytes.Equal(secondMeta, firstMeta) {
		t.Fatalf("current-version reopen changed migrated row: source %q→%q metaEqual=%v",
			firstSource, secondSource, bytes.Equal(firstMeta, secondMeta))
	}
	if version, err := readUserVersion(reopened.db); err != nil || version != currentSchemaVersion {
		t.Fatalf("reopened user_version = %d (err %v), want %d", version, err, currentSchemaVersion)
	}
}

func TestOpenV4SemanticSourceMigrationFailureRollsBackEveryPage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("create current store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close current store: %v", err)
	}

	good, err := encodeMeta(map[string]any{
		edgeSemanticSourceMetaMarker: "goanalysis",
		"kept":                       "rollback",
	})
	if err != nil {
		t.Fatalf("encode good Meta: %v", err)
	}
	// Valid flat header + one map entry/key, but no value tag. It passes the
	// SQLite marker prefilter and fails only when the second keyset page decodes
	// it, after the first page's batched UPDATEs have executed in the same tx.
	malformed := append([]byte{metaFlatMagic0, metaFlatVersion, 1, byte(len(edgeSemanticSourceMetaMarker))}, edgeSemanticSourceMetaMarker...)
	withRawDB(t, path, func(db *sql.DB) {
		seedRepeatedMigrationEdges(t, db, "rollback-good", edgeSemanticSourceMigrationPageRows, good)
		insertMigrationEdge(t, db, "rollback-bad", malformed)
		if _, err := db.Exec(`PRAGMA user_version = 4`); err != nil {
			t.Fatalf("mark store v4: %v", err)
		}
	})

	failed, err := Open(path)
	if failed != nil {
		_ = failed.Close()
	}
	if err == nil {
		t.Fatal("expected malformed second-page Meta to fail the v4 migration")
	}
	if !strings.Contains(err.Error(), "decode edge") || !errors.Is(err, errMetaTruncated) {
		t.Fatalf("migration error = %v, want wrapped decode/truncation error", err)
	}

	withRawDB(t, path, func(db *sql.DB) {
		version, err := readUserVersion(db)
		if err != nil {
			t.Fatalf("read rolled-back version: %v", err)
		}
		if version != 4 {
			t.Fatalf("user_version after failed migration = %d, want 4", version)
		}
		var promoted int
		if err := db.QueryRow(`SELECT COUNT(*) FROM edges WHERE semantic_source IS NOT NULL`).Scan(&promoted); err != nil {
			t.Fatalf("count rolled-back provenance: %v", err)
		}
		if promoted != 0 {
			t.Fatalf("failed migration committed %d first-page rows", promoted)
		}
		if _, err := db.Exec(`DELETE FROM edges WHERE from_id = 'rollback-bad-from'`); err != nil {
			t.Fatalf("remove malformed fixture for retry: %v", err)
		}
	})

	retried, err := Open(path)
	if err != nil {
		t.Fatalf("retry clean v4 migration: %v", err)
	}
	defer retried.Close()
	var promoted int
	if err := retried.db.QueryRow(`SELECT COUNT(*) FROM edges WHERE semantic_source = 'goanalysis'`).Scan(&promoted); err != nil {
		t.Fatalf("count retried provenance: %v", err)
	}
	if promoted != edgeSemanticSourceMigrationPageRows {
		t.Fatalf("retry promoted %d rows, want %d", promoted, edgeSemanticSourceMigrationPageRows)
	}
	if version, err := readUserVersion(retried.db); err != nil || version != currentSchemaVersion {
		t.Fatalf("retry user_version = %d (err %v), want %d", version, err, currentSchemaVersion)
	}
}

func TestBackfillEdgeSemanticSourcesRejectsInvalidLimits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	tx, err := store.writerDB.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback() //nolint:errcheck
	_, err = backfillEdgeSemanticSourcesWithLimits(tx, edgeSemanticSourceMigrationLimits{})
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("rows=%d", 0)) {
		t.Fatalf("invalid limits error = %v", err)
	}
}
