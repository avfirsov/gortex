package indexer

import (
	"testing"
	"time"
)

func TestWatcherClaimPendingTimerRejectsSupersededCallback(t *testing.T) {
	stale := time.NewTimer(time.Hour)
	current := time.NewTimer(time.Hour)
	t.Cleanup(func() {
		stale.Stop()
		current.Stop()
	})

	w := &Watcher{pending: map[string]*time.Timer{"result.json": current}}

	if w.claimPendingTimer("result.json", &stale) {
		t.Fatal("superseded timer claimed the pending edit")
	}
	if got := w.pending["result.json"]; got != current {
		t.Fatal("superseded timer removed the current pending edit")
	}
	if !w.claimPendingTimer("result.json", &current) {
		t.Fatal("current timer did not claim the pending edit")
	}
	if _, ok := w.pending["result.json"]; ok {
		t.Fatal("claimed timer remained pending")
	}
}
