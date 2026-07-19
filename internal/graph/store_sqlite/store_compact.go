package store_sqlite

// One-time boot compaction support.
//
// The graph store only ever grows on disk: deleted rows (a purged repo, the
// duplicate-collapse migration, resolver cleanups) return their pages to
// SQLite's freelist, where future writes reuse them — but nothing short of
// VACUUM returns them to the filesystem. A long-lived store that shed a large
// fraction of its rows can therefore pin gigabytes of dead file forever (a
// live store sat at 64% freelist — 4.4 GB reclaimable in a 6.8 GB file).
// These methods give the daemon the numbers to decide whether that one-time
// rewrite is worth it, and the lever to run it. The policy (thresholds, disk
// headroom, kill-switch) deliberately lives with the caller: the store cannot
// know whether minutes of exclusive I/O are acceptable right now.

// Path returns the on-disk database file path. Empty when the store was not
// opened from a file — callers using it to reason about the underlying
// filesystem (disk-headroom checks) must treat "" as "unknown, don't".
func (s *Store) Path() string {
	return s.dbPath
}

// CompactStats reports how much of the database file is reclaimable dead
// space: freeBytes is the freelist (freelist_count × page_size), totalBytes
// the whole main file (page_count × page_size). Zeros on any pragma error —
// a read failing here is the same teardown race panicOnFatal swallows, and
// "nothing reclaimable" is the answer that makes every caller do nothing.
// The -wal file is excluded on purpose: the checkpoint loop already bounds
// it, and VACUUM only rewrites the main file.
func (s *Store) CompactStats() (freeBytes, totalBytes int64) {
	var pageSize, pageCount, freePages int64
	if err := s.db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		return 0, 0
	}
	if err := s.db.QueryRow(`PRAGMA page_count`).Scan(&pageCount); err != nil {
		return 0, 0
	}
	if err := s.db.QueryRow(`PRAGMA freelist_count`).Scan(&freePages); err != nil {
		return 0, 0
	}
	return freePages * pageSize, pageCount * pageSize
}

// Compact rewrites the database file (VACUUM), returning freelist pages to
// the filesystem, then drains the write-ahead log with a TRUNCATE checkpoint
// so the rewrite's WAL traffic doesn't linger as a second oversized file.
//
// Cost model callers must respect: VACUUM copies the live content into a
// temporary database (up to a full extra copy on the same filesystem) and
// needs exclusive access — it blocks Go-side writers via writeMu here, and a
// concurrent reader on another pooled connection makes SQLite wait out
// busy_timeout and then fail. That failure is clean (the store is untouched,
// freelist pages remain reusable), which is why the daemon treats a Compact
// error as skip-and-continue rather than fatal.
func (s *Store) Compact() error {
	s.writeMu.Lock()
	_, err := s.writerDB.Exec(`VACUUM`)
	s.writeMu.Unlock()
	if err != nil {
		return err
	}
	return s.CheckpointWAL()
}
