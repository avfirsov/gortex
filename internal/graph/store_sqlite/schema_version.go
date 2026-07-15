package store_sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
)

// Schema versioning for the graph store.
//
// Unlike the sidecar (which holds irreplaceable user data and must migrate in
// place), the graph store is a DERIVED CACHE: every row is reconstructable by
// re-indexing the source. So the cheapest *always-correct* reaction to a schema
// change an old on-disk DB can't satisfy is to drop the file and let the daemon
// rebuild it on the next index. A migration may therefore declare rebuild=true
// instead of writing an in-place transform that would have to re-derive the new
// data from source anyway. In-place steps remain the cheap path for purely
// mechanical changes (a new index, a denormalisation, a column with a
// computable default) that spare a large repo a multi-minute reindex.
//
// The whole mechanism keys off SQLite's built-in PRAGMA user_version, read on
// Open before schemaSQL runs. There is no separate version table.
//
// Concurrency: the daemon holds an exclusive flock on <store>.lock around Open
// (see serverstack.NewSharedServer), so reading the version, wiping the file,
// and stamping it cannot race another process. That is why — unlike the
// sidecar — this path needs no BEGIN IMMEDIATE / busy-loop handling.

// currentSchemaVersion is the version a fully-reconciled store reports via
// PRAGMA user_version. Bump it whenever schemaSQL's typed-column shape or an
// index changes in a way an old on-disk DB would not already have, and append a
// matching schemaMigrations entry describing how to bring an older store
// forward (in place, or by rebuild).
const currentSchemaVersion = 4

// schemaMigration is one forward step. Exactly one strategy applies:
//   - rebuild=true: the change introduces structure/data that can only come
//     from re-indexing the source; an older store is wiped and rebuilt.
//   - inPlace!=nil: the change is mechanically derivable from the existing
//     store and is applied in a transaction with no reindex.
//
// Steps are append-only and ascending; never edit or renumber a shipped one.
// Any inPlace step must be idempotent (IF NOT EXISTS / ADD COLUMN guarded).
type schemaMigration struct {
	version int
	name    string
	inPlace func(tx *sql.Tx) error
	rebuild bool
}

// schemaMigrations is the ordered, forward-only registry. Version 1 is the
// implicit baseline (no entry): a v1 store is reconciled entirely by schemaSQL's
// idempotent CREATE ... IF NOT EXISTS plus ensureNodeColumns, so any
// pre-versioning database baseline-stamps to v1 without a rebuild. Append
// entries for version 2 and up as the schema evolves.
var schemaMigrations = []schemaMigration{
	{version: 2, name: "dedupe fn-value placeholder edges", inPlace: dedupeFnValuePlaceholderEdges},
	// Versions through v2 wrote node updates with INSERT OR REPLACE. REPLACE
	// has delete semantics and can invalidate incident-edge integrity when
	// foreign-key enforcement is enabled by a host/connection. Deleted edges
	// cannot be reconstructed from the remaining graph rows, so this is an
	// explicit source-reindex boundary rather than a misleading in-place fix.
	{version: 3, name: "restore topology after node replace writes", rebuild: true},
	{version: 4, name: "add normalized analysis generations", inPlace: createAnalysisGenerationTables},
}

// createAnalysisGenerationTables is the explicit v4 in-place migration.
// schemaSQL runs first and is intentionally idempotent, so this is a no-op on
// fresh stores and a defensive create on older stores opened by migration
// tests or future alternate open paths.
func createAnalysisGenerationTables(tx *sql.Tx) error {
	if _, err := tx.Exec(analysisGenerationSchemaSQL); err != nil {
		return err
	}
	// Builds used during development briefly created a blob-only cache under
	// schema v3. It was never released; remove the artifact instead of carrying
	// a conversion or compatibility API into v4.
	_, err := tx.Exec(`DROP TABLE IF EXISTS analysis_cache`)
	return err
}

// dedupeFnValuePlaceholderEdges collapses duplicate function-as-value gate
// placeholder edges (graph.FnValuePlaceholderMarker, `unresolved::fnvalue::
// <name>`) to one row per (from_id, to_id), keeping the MIN(id) survivor. The
// capture path now dedups per (from, name) before it emits, but stores written
// earlier accumulated one placeholder per call site — a live store held
// millions — and EdgesWithUnresolvedTarget plus the resolver's terminal
// reconcile materialised every one on each warm restart, the dominant warmup
// heap transient this step drains. The keep set is small (tens of thousands of
// distinct pairs), so the NOT IN materialisation is cheap; the ph filter rides
// the edges_by_to(to_id) range for the bare form and the is_unresolved index for
// the multi-repo infix form. Idempotent: a second run finds no duplicates. Freed
// pages return to the freelist and are reused by later writes; the file itself
// shrinks only under a manual VACUUM, deliberately out of scope for a derived
// cache that reclaims the space on its own.
func dedupeFnValuePlaceholderEdges(tx *sql.Tx) error {
	_, err := tx.Exec(`
WITH ph AS (
    SELECT id, from_id, to_id FROM edges
    WHERE (to_id >= 'unresolved::fnvalue::' AND to_id < 'unresolved::fnvalue:;')
       OR (is_unresolved = 1 AND to_id LIKE '%::unresolved::fnvalue::%')
), keep AS (
    SELECT MIN(id) AS id FROM ph GROUP BY from_id, to_id
)
DELETE FROM edges WHERE id IN (SELECT id FROM ph) AND id NOT IN (SELECT id FROM keep)`)
	return err
}

// schemaPlan is the decision planSchemaMigration derives from the stored
// PRAGMA user_version. It mutates nothing on its own.
type schemaPlan struct {
	wipe    bool              // drop the on-disk DB and rebuild from source
	inPlace []schemaMigration // ordered in-place steps to run after schemaSQL
	stamp   bool              // write currentSchemaVersion once reconciled
}

// planSchemaMigrationWith decides how to reconcile a store at the stored
// PRAGMA user_version to current, given the migration registry. It mutates
// nothing. Open passes (currentSchemaVersion, schemaMigrations); tests pass
// fixtures.
func planSchemaMigrationWith(stored, current int, migrations []schemaMigration) schemaPlan {
	switch {
	case stored == current:
		return schemaPlan{} // up to date, nothing to do
	case stored > current:
		// Written by a newer build than this binary understands; the shape may
		// have changed under us. For a cache the safe move is to rebuild.
		return schemaPlan{wipe: true, stamp: true}
	case stored == 0:
		// Fresh DB, or a pre-versioning store of unknown shape. schemaSQL's
		// idempotent CREATE ... IF NOT EXISTS plus ensureNodeColumns /
		// ensureEdgeColumns reconcile the base shape either way, so a stored==0
		// store needs a wipe only when a pending step is a REBUILD whose data can
		// only come from re-indexing source. With nothing pending, stamp; with
		// only in-place steps pending, run them and stamp — an in-place step is
		// idempotent and mechanically derivable, so it upgrades a pre-versioning
		// store in place (preserving its rows) exactly as it upgrades a known
		// prior version. Wiping a stored==0 store on any migration instead would
		// force every non-daemon Open (tests, read-only tools) to pass WithRebuild
		// the moment the first migration ships.
		pending := pendingBetween(0, current, migrations)
		if len(pending) == 0 {
			return schemaPlan{stamp: true}
		}
		if anyRebuild(pending) {
			return schemaPlan{wipe: true, stamp: true}
		}
		return schemaPlan{inPlace: pending, stamp: true}
	default: // 0 < stored < current: a known prior version
		pending := pendingBetween(stored, current, migrations)
		if anyRebuild(pending) {
			return schemaPlan{wipe: true, stamp: true}
		}
		return schemaPlan{inPlace: pending, stamp: true}
	}
}

func pendingBetween(stored, current int, migrations []schemaMigration) []schemaMigration {
	var out []schemaMigration
	for _, m := range migrations {
		if m.version > stored && m.version <= current {
			out = append(out, m)
		}
	}
	return out
}

func anyRebuild(ms []schemaMigration) bool {
	for _, m := range ms {
		if m.rebuild {
			return true
		}
	}
	return false
}

// validateSchemaMigrations checks the registry is well-formed. A test asserts
// this against the shipped (currentSchemaVersion, schemaMigrations) so the
// dangerous mistake — bumping currentSchemaVersion without appending a matching
// entry — fails CI instead of silently baseline-stamping an un-migrated store
// to the new version at runtime. Rules:
//   - versions are >= 2 (v1 is the implicit baseline, never an entry) and
//     strictly ascending;
//   - each step sets exactly one strategy (inPlace xor rebuild);
//   - the highest version equals current, so the registry actually defines how
//     to reach it. An empty registry is valid only at version 1.
func validateSchemaMigrations(current int, migs []schemaMigration) error {
	if len(migs) == 0 {
		if current != 1 {
			return fmt.Errorf("schema version %d has no migrations: only v1 may have an empty registry", current)
		}
		return nil
	}
	prev := 0
	for i, m := range migs {
		if m.version < 2 {
			return fmt.Errorf("migration %q has version %d: entries must be >= 2 (v1 is the implicit baseline)", m.name, m.version)
		}
		if i > 0 && m.version <= prev {
			return fmt.Errorf("migrations must be strictly ascending: v%d (%s) does not follow v%d", m.version, m.name, prev)
		}
		if (m.inPlace != nil) == m.rebuild {
			return fmt.Errorf("migration v%d (%s) must set exactly one of inPlace / rebuild", m.version, m.name)
		}
		prev = m.version
	}
	if prev != current {
		return fmt.Errorf("highest migration version %d != currentSchemaVersion %d: a version bump needs a matching migration entry", prev, current)
	}
	return nil
}

// readUserVersion reads PRAGMA user_version (0 on a fresh database).
func readUserVersion(db *sql.DB) (int, error) {
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return 0, err
	}
	return v, nil
}

// setUserVersion stamps the schema version. PRAGMA takes no bound parameters;
// v is an int we control, so the format is safe.
func setUserVersion(db *sql.DB, v int) error {
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", v)); err != nil {
		return err
	}
	return nil
}

// applyInPlaceMigrations runs the in-place steps in a single transaction.
func applyInPlaceMigrations(db *sql.DB, steps []schemaMigration) error {
	if len(steps) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit succeeds
	for _, m := range steps {
		if err := m.inPlace(tx); err != nil {
			return fmt.Errorf("schema migration v%d (%s): %w", m.version, m.name, err)
		}
	}
	return tx.Commit()
}

// removeStoreFiles deletes the SQLite database and its companions. A missing
// file is not an error. Never called for ":memory:".
//
// The suffix list covers the files the DSN's journal_mode(WAL) produces (-wal,
// -shm) plus the rollback -journal a non-WAL fallback would use; keep it in
// sync if the journal_mode in Open's DSN ever changes.
func removeStoreFiles(path string) error {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err := os.Remove(path + suffix); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", path+suffix, err)
		}
	}
	return nil
}

// isMemoryPath reports whether path is an in-process SQLite database (no file
// on disk to wipe, always built fresh by schemaSQL).
func isMemoryPath(path string) bool {
	return strings.Contains(path, ":memory:")
}
