package indexer

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWatcherStopReturnsAfterPreLoopStartFailure(t *testing.T) {
	_, _, watcher := inertTestWatcher(t, "main.go", "package main\n\nfunc Value() int { return 0 }\n")

	require.Error(t, watcher.Start(nil))
	require.NoError(t, watcher.Stop())
}

func TestWatcherConcurrentStopAndPreLoopStartFailureSignalOnce(t *testing.T) {
	_, _, watcher := inertTestWatcher(t, "main.go", "package main\n\nfunc Value() int { return 0 }\n")
	failureReady := make(chan struct{})
	releaseFailure := make(chan struct{})
	watcher.startFailureBeforeSignal = func() {
		close(failureReady)
		<-releaseFailure
	}
	stopAdmissionClosed := make(chan struct{})
	watcher.stopAdmissionClosed = func() { close(stopAdmissionClosed) }

	startDone := make(chan error, 1)
	go func() { startDone <- watcher.Start(nil) }()
	<-failureReady
	stopDone := make(chan error, 1)
	go func() { stopDone <- watcher.Stop() }()
	<-stopAdmissionClosed
	select {
	case err := <-stopDone:
		t.Fatalf("Stop returned before the failed Start published loop termination: %v", err)
	default:
	}

	close(releaseFailure)
	require.Error(t, <-startDone)
	require.NoError(t, <-stopDone)

	// A late loop owner observes done and publishes through the same once. This
	// would panic here if Start's error path and loop teardown could both close
	// stopped directly.
	watcher.loop()
	select {
	case <-watcher.stopped:
	default:
		t.Fatal("watcher termination was not published")
	}
}

func TestWatcherStopJoinsClaimedPointMutation(t *testing.T) {
	dir, _, watcher := inertTestWatcher(t, "main.go", "package main\n\nfunc Value() int { return 0 }\n")
	watcher.degradedNoFsnotify = true
	watcher.config.DebounceMs = 1
	path := filepath.Join(dir, "main.go")

	claimed := make(chan struct{})
	release := make(chan struct{})
	watcher.pointMutationClaimed = func(string) {
		close(claimed)
		<-release
	}
	admissionClosed := make(chan struct{})
	watcher.stopAdmissionClosed = func() { close(admissionClosed) }

	ticket, err := watcher.EnqueueFileMutation(context.Background(), path)
	require.NoError(t, err)
	require.NotNil(t, ticket)
	<-claimed

	stopDone := make(chan error, 1)
	go func() { stopDone <- watcher.Stop() }()
	<-admissionClosed
	select {
	case err := <-stopDone:
		t.Fatalf("Stop returned while a claimed point mutation still owned graph work: %v", err)
	default:
	}

	close(release)
	require.NoError(t, <-stopDone)
	result, ok := <-ticket.Done
	require.True(t, ok)
	require.ErrorIs(t, result.Err, errWatcherStopped)
}

func TestWatcherEnqueueAdmissionCannotCrossStop(t *testing.T) {
	dir, _, watcher := inertTestWatcher(t, "main.go", "package main\n\nfunc Value() int { return 0 }\n")
	watcher.degradedNoFsnotify = true
	path := filepath.Join(dir, "main.go")

	beforeAdmission := make(chan struct{})
	releaseAdmission := make(chan struct{})
	watcher.mutationBeforeAdmission = func() {
		close(beforeAdmission)
		<-releaseAdmission
	}
	admissionClosed := make(chan struct{})
	watcher.stopAdmissionClosed = func() { close(admissionClosed) }

	type enqueueResult struct {
		ticket *MutationTicket
		err    error
	}
	enqueued := make(chan enqueueResult, 1)
	go func() {
		ticket, err := watcher.EnqueueFileMutation(context.Background(), path)
		enqueued <- enqueueResult{ticket: ticket, err: err}
	}()
	<-beforeAdmission

	stopDone := make(chan error, 1)
	go func() { stopDone <- watcher.Stop() }()
	<-admissionClosed
	close(releaseAdmission)

	result := <-enqueued
	require.Nil(t, result.ticket)
	require.ErrorIs(t, result.err, errWatcherStopped)
	require.NoError(t, <-stopDone)
	watcher.mu.Lock()
	require.Empty(t, watcher.pending)
	require.Empty(t, watcher.mutationWaiters)
	watcher.mu.Unlock()
}

func TestWatcherStopJoinsAuxiliaryGraphWorkers(t *testing.T) {
	tests := []struct {
		name     string
		schedule func(*Watcher)
		install  func(*Watcher, func())
	}{
		{
			name: "overflow",
			schedule: func(w *Watcher) {
				w.triggerOverflowReconcile("test")
			},
			install: func(w *Watcher, fn func()) { w.reconcileFn = fn },
		},
		{
			name: "directory",
			schedule: func(w *Watcher) {
				w.enqueueDirScan(filepath.Join(w.indexer.rootPath, "new-dir"))
			},
			install: func(w *Watcher, fn func()) {
				w.scanFn = func(map[string]struct{}) { fn() }
			},
		},
		{
			name: "reresolve",
			schedule: func(w *Watcher) {
				w.enqueueReresolve(filepath.Join(w.indexer.rootPath, "main.go"))
			},
			install: func(w *Watcher, fn func()) {
				w.reresolveFn = func(map[string]struct{}) { fn() }
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, watcher := inertTestWatcher(t, "main.go", "package main\n\nfunc Value() int { return 0 }\n")
			watcher.degradedNoFsnotify = true
			entered := make(chan struct{})
			release := make(chan struct{})
			tt.install(watcher, func() {
				close(entered)
				<-release
			})
			admissionClosed := make(chan struct{})
			watcher.stopAdmissionClosed = func() { close(admissionClosed) }

			tt.schedule(watcher)
			<-entered
			stopDone := make(chan error, 1)
			go func() { stopDone <- watcher.Stop() }()
			<-admissionClosed
			select {
			case err := <-stopDone:
				t.Fatalf("Stop returned before %s worker left graph work: %v", tt.name, err)
			default:
			}
			close(release)
			require.NoError(t, <-stopDone)
		})
	}
}

func TestWatcherAdmissionAfterStopIsRejected(t *testing.T) {
	_, _, watcher := inertTestWatcher(t, "main.go", "package main\n\nfunc Value() int { return 0 }\n")
	watcher.degradedNoFsnotify = true
	require.NoError(t, watcher.Stop())

	ticket, err := watcher.EnqueueFileMutation(context.Background(), filepath.Join(watcher.indexer.rootPath, "main.go"))
	require.Nil(t, ticket)
	require.ErrorIs(t, err, errWatcherStopped)
}
