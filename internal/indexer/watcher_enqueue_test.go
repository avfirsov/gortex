package indexer

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWatcherEnqueueFileMutationLatestGenerationWins(t *testing.T) {
	dir, _, watcher := inertTestWatcher(t, "main.go", "package main\n\nfunc value() int { return 0 }\n")
	path := filepath.Join(dir, "main.go")

	// Hold the patch lane so all three debounce callbacks can become runnable.
	// Only the latest generation may cross the second generation check after the
	// lane is released.
	watcher.patchMu.Lock()
	locked := true
	defer func() {
		if locked {
			watcher.patchMu.Unlock()
		}
	}()

	tickets := make([]*MutationTicket, 0, 3)
	for i := 1; i <= 3; i++ {
		writeTestFile(t, path, "package main\n\nfunc value() int { return "+string(rune('0'+i))+" }\n")
		ticket, err := watcher.EnqueueFileMutation(context.Background(), path)
		require.NoError(t, err)
		require.NotNil(t, ticket)
		tickets = append(tickets, ticket)
		time.Sleep(25 * time.Millisecond)
	}

	watcher.patchMu.Unlock()
	locked = false

	var appliedGeneration uint64
	for _, ticket := range tickets {
		select {
		case result := <-ticket.Done:
			require.NoError(t, result.Err)
			require.True(t, result.Reindexed)
			require.Equal(t, ticket.Generation, result.RequestedGeneration)
			require.GreaterOrEqual(t, result.AppliedGeneration, ticket.Generation)
			if appliedGeneration == 0 {
				appliedGeneration = result.AppliedGeneration
			} else {
				require.Equal(t, appliedGeneration, result.AppliedGeneration)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("mutation ticket generation %d did not complete", ticket.Generation)
		}
	}
	require.Equal(t, tickets[len(tickets)-1].Generation, appliedGeneration)

	// Give stale callbacks time to acquire the lane and reject themselves.
	time.Sleep(50 * time.Millisecond)
	history := watcher.History()
	require.Len(t, history, 1)
	require.Equal(t, ChangeModified, history[0].Kind)
}

func TestWatcherEnqueueFileMutationCancelledAdmissionLeavesNoWork(t *testing.T) {
	dir, _, watcher := inertTestWatcher(t, "main.go", "package main\n\nfunc value() int { return 0 }\n")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ticket, err := watcher.EnqueueFileMutation(ctx, filepath.Join(dir, "main.go"))
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, ticket)

	watcher.mu.Lock()
	pending := len(watcher.pending)
	generation := watcher.nextGeneration
	watcher.mu.Unlock()
	require.Zero(t, pending)
	require.Zero(t, generation)
}
