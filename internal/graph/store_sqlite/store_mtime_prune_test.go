package store_sqlite_test

import (
	"reflect"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// TestReplaceFileMtimesPrunesDeleted is the regression for the warm-restart
// "nothing changed but full re-track" bug: the full-index persist path must
// REPLACE a repo's mtime set, not union into it. An upsert-only persist
// leaves rows for files deleted since the last index behind, and warm-restart
// reconcile then detects them as phantom deletions on every restart — forcing
// a full re-track that never converges.
func TestReplaceFileMtimesPrunesDeleted(t *testing.T) {
	s := openTestStore(t)

	// Assert the store advertises the capability the indexer probes for.
	var _ graph.FileMtimeReplacer = s
	var _ graph.FileMtimeDeleter = s

	// First index: three files persisted.
	require := func(err error, what string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	require(s.BulkSetFileMtimes("repoA", map[string]int64{
		"a/one.go":   100,
		"a/two.go":   200,
		"a/three.go": 300,
	}), "seed BulkSetFileMtimes")

	// A different repo whose rows must never be touched by repoA writes.
	require(s.BulkSetFileMtimes("repoB", map[string]int64{"b/x.go": 999}), "seed repoB")

	// Second index: two.go was deleted on disk, four.go is new, three.go
	// changed. The authoritative snapshot is {one, three', four}.
	require(s.ReplaceFileMtimes("repoA", map[string]int64{
		"a/one.go":   100,
		"a/three.go": 350, // changed
		"a/four.go":  400, // new
	}), "ReplaceFileMtimes")

	want := map[string]int64{
		"a/one.go":   100,
		"a/three.go": 350,
		"a/four.go":  400,
	}
	got := s.LoadFileMtimes("repoA")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after ReplaceFileMtimes = %v, want %v (a/two.go must be pruned)", got, want)
	}
	if _, stillThere := got["a/two.go"]; stillThere {
		t.Fatal("a/two.go was deleted on disk but its mtime row survived the replace — phantom deletion bug")
	}

	// Repo isolation.
	if b := s.LoadFileMtimes("repoB"); !reflect.DeepEqual(b, map[string]int64{"b/x.go": 999}) {
		t.Fatalf("repoB rows disturbed by repoA replace: %v", b)
	}

	// Empty input is a deliberate no-op: it must NEVER wipe a repo's set.
	require(s.ReplaceFileMtimes("repoA", nil), "ReplaceFileMtimes(nil)")
	if got := s.LoadFileMtimes("repoA"); !reflect.DeepEqual(got, want) {
		t.Fatalf("ReplaceFileMtimes(nil) wiped the repo: %v", got)
	}
}

// TestDeleteFileMtimes covers the incremental-reindex sibling: the watcher /
// incremental path drops just the deleted paths so the persisted set stays in
// step with the live graph without a full replace.
func TestDeleteFileMtimes(t *testing.T) {
	s := openTestStore(t)

	if err := s.BulkSetFileMtimes("repoA", map[string]int64{
		"a/one.go":   100,
		"a/two.go":   200,
		"a/three.go": 300,
		"a/four.go":  400,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.BulkSetFileMtimes("repoB", map[string]int64{"b/keep.go": 7}); err != nil {
		t.Fatalf("seed repoB: %v", err)
	}

	// Delete two existing paths and one that was never recorded (harmless).
	if err := s.DeleteFileMtimes("repoA", []string{"a/two.go", "a/four.go", "a/never.go"}); err != nil {
		t.Fatalf("DeleteFileMtimes: %v", err)
	}

	want := map[string]int64{"a/one.go": 100, "a/three.go": 300}
	if got := s.LoadFileMtimes("repoA"); !reflect.DeepEqual(got, want) {
		t.Fatalf("after delete = %v, want %v", got, want)
	}

	// Repo isolation: same-named delete on repoA must not touch repoB.
	if b := s.LoadFileMtimes("repoB"); !reflect.DeepEqual(b, map[string]int64{"b/keep.go": 7}) {
		t.Fatalf("repoB disturbed: %v", b)
	}

	// Empty input is a no-op.
	if err := s.DeleteFileMtimes("repoA", nil); err != nil {
		t.Fatalf("DeleteFileMtimes(nil): %v", err)
	}
	if got := s.LoadFileMtimes("repoA"); !reflect.DeepEqual(got, want) {
		t.Fatalf("DeleteFileMtimes(nil) changed the set: %v", got)
	}
}
