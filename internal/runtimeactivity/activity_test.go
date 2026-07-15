package runtimeactivity

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTrackerRunIfQuietRejectsActiveAndRecentWork(t *testing.T) {
	tracker := NewTracker()
	tracker.Begin("watcher")
	if ran, retry := tracker.RunIfQuiet(0, func() { t.Fatal("ran while active") }); ran || retry != 0 {
		t.Fatalf("active RunIfQuiet = (%v, %v), want (false, 0)", ran, retry)
	}
	tracker.End("watcher")

	const quiet = 20 * time.Millisecond
	if ran, retry := tracker.RunIfQuiet(quiet, nil); ran || retry <= 0 || retry > quiet {
		t.Fatalf("recent RunIfQuiet = (%v, %v), want bounded retry", ran, retry)
	}
	time.Sleep(quiet + 5*time.Millisecond)
	called := false
	if ran, retry := tracker.RunIfQuiet(quiet, func() { called = true }); !ran || retry != 0 || !called {
		t.Fatalf("quiet RunIfQuiet = (%v, %v, called=%v), want (true, 0, true)", ran, retry, called)
	}
}

func TestTrackerExclusiveMaintenanceBlocksNewWork(t *testing.T) {
	tracker := NewTracker()
	entered := make(chan struct{})
	release := make(chan struct{})
	maintenanceDone := make(chan struct{})
	go func() {
		defer close(maintenanceDone)
		ran, _ := tracker.RunIfQuiet(0, func() {
			close(entered)
			<-release
		})
		if !ran {
			t.Error("maintenance did not run")
		}
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("maintenance did not enter")
	}

	workStarted := make(chan struct{})
	go func() {
		tracker.Begin("mcp")
		close(workStarted)
		tracker.End("mcp")
	}()
	select {
	case <-workStarted:
		t.Fatal("work started during exclusive maintenance")
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	select {
	case <-maintenanceDone:
	case <-time.After(time.Second):
		t.Fatal("maintenance did not finish")
	}
	select {
	case <-workStarted:
	case <-time.After(time.Second):
		t.Fatal("work remained blocked after maintenance")
	}
}

func TestTrackerIdleHookCoversLostWakeupTransition(t *testing.T) {
	tracker := NewTracker()
	var calls atomic.Int64
	called := make(chan struct{}, 2)
	unregister := tracker.RegisterIdleHook(func(kind string) {
		if kind != "analysis" {
			t.Errorf("idle kind = %q, want analysis", kind)
		}
		calls.Add(1)
		called <- struct{}{}
	})
	defer unregister()

	tracker.Begin("analysis")
	tracker.End("analysis")
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("idle hook was lost")
	}
	tracker.Begin("analysis")
	tracker.End("analysis")
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("second idle hook was lost")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("idle hook calls = %d, want 2", got)
	}
}

func TestTrackerConcurrentKindsBalance(t *testing.T) {
	tracker := NewTracker()
	const workers = 32
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := range workers {
		go func() {
			defer wg.Done()
			kind := "watcher"
			if i%2 == 0 {
				kind = "mcp"
			}
			tracker.Begin(kind)
			time.Sleep(time.Millisecond)
			tracker.End(kind)
		}()
	}
	wg.Wait()
	snapshot := tracker.Snapshot()
	if snapshot.Active != 0 || len(snapshot.ByKind) != 0 {
		t.Fatalf("unbalanced snapshot: %+v", snapshot)
	}
	if snapshot.Epoch != workers*2 {
		t.Fatalf("epoch = %d, want %d", snapshot.Epoch, workers*2)
	}
}
