package indexer

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMultiWatcherEnqueueFileMutationIgnoresUnrelatedDegradation(t *testing.T) {
	rootA, _, watcherA := inertTestWatcher(t, "a.go", "package a\n\nfunc A() {}\n")
	rootB, _, watcherB := inertTestWatcher(t, "b.go", "package b\n\nfunc B() {}\n")

	watcherA.degradedMu.Lock()
	watcherA.degradedReason = "overflow"
	watcherA.degradedMu.Unlock()

	multi := &MultiIndexer{repos: map[string]*RepoMetadata{
		"a": {RepoPrefix: "a", RootPath: rootA},
		"b": {RepoPrefix: "b", RootPath: rootB},
	}}
	watcher := &MultiWatcher{
		watchers: map[string]*Watcher{"a": watcherA, "b": watcherB},
		started:  map[string]bool{"a": true, "b": true},
		multi:    multi,
	}

	require.NotEmpty(t, watcher.DegradedReason())
	ticket, err := watcher.EnqueueFileMutation(context.Background(), filepath.Join(rootB, "b.go"))
	require.NoError(t, err)
	require.NotNil(t, ticket)
	select {
	case result := <-ticket.Done:
		require.NoError(t, result.Err)
		require.True(t, result.Reindexed)
		require.Equal(t, ticket.Generation, result.RequestedGeneration)
		require.GreaterOrEqual(t, result.AppliedGeneration, ticket.Generation)
	case <-time.After(2 * time.Second):
		t.Fatalf("mutation ticket generation %d did not complete", ticket.Generation)
	}
	require.Len(t, watcherB.History(), 1)
	require.Empty(t, watcherA.History())
}

func TestMultiWatcherEnqueueFileMutationRejectsUnstartedOwner(t *testing.T) {
	root, _, repoWatcher := inertTestWatcher(t, "main.go", "package main\n\nfunc main() {}\n")
	multi := &MultiIndexer{repos: map[string]*RepoMetadata{
		"repo": {RepoPrefix: "repo", RootPath: root},
	}}
	watcher := &MultiWatcher{
		watchers: map[string]*Watcher{"repo": repoWatcher},
		started:  map[string]bool{"repo": false},
		multi:    multi,
	}

	ticket, err := watcher.EnqueueFileMutation(context.Background(), filepath.Join(root, "main.go"))
	require.NoError(t, err)
	require.Nil(t, ticket)
	repoWatcher.mu.Lock()
	pending := len(repoWatcher.pending)
	repoWatcher.mu.Unlock()
	require.Zero(t, pending)
}
