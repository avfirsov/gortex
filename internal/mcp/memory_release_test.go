package mcp

import (
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func waitForMemoryReleaseSchedulerIdle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !osMemoryReleaseScheduled.Load() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("memory release scheduler did not become idle")
}

func waitForScheduledMemoryRelease(t *testing.T, logs *observer.ObservedLogs, wantLogs int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !osMemoryReleaseScheduled.Load() && logs.Len() >= wantLogs {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("memory release did not finish: scheduled=%v logs=%d, want logs >= %d",
		osMemoryReleaseScheduled.Load(), logs.Len(), wantLogs)
}

func TestScheduleOSMemoryReleaseAfterBurstDisabled(t *testing.T) {
	waitForMemoryReleaseSchedulerIdle(t)
	t.Setenv("GORTEX_DAEMON_MEMRELEASE", "false")

	scheduleOSMemoryReleaseAfterBurst(zap.NewNop(), "disabled")
	if osMemoryReleaseScheduled.Load() {
		t.Fatal("disabled memory release reserved the scheduler")
	}
}

func TestScheduleOSMemoryReleaseAfterBurstCoalescesAndRearms(t *testing.T) {
	waitForMemoryReleaseSchedulerIdle(t)
	t.Setenv("GORTEX_DAEMON_MEMRELEASE", "1")
	t.Setenv("GORTEX_DAEMON_MEMRELEASE_MIN_MB", "0")
	t.Setenv("GORTEX_DAEMON_MEMRELEASE_COOLDOWN", "0")

	core, observed := observer.New(zap.DebugLevel)
	logger := zap.New(core)

	const callers = 32
	var wg sync.WaitGroup
	wg.Add(callers)
	for range callers {
		go func() {
			defer wg.Done()
			scheduleOSMemoryReleaseAfterBurst(logger, "first-wave")
		}()
	}
	wg.Wait()
	if !osMemoryReleaseScheduled.Load() {
		t.Fatal("enabled memory release was not scheduled")
	}
	waitForScheduledMemoryRelease(t, observed, 1)
	if got := observed.Len(); got != 1 {
		t.Fatalf("coalesced release logs = %d, want 1", got)
	}
	if got := observed.All()[0].ContextMap()["reason"]; got != "first-wave" {
		t.Fatalf("first release reason = %v, want first-wave", got)
	}

	scheduleOSMemoryReleaseAfterBurst(logger, "second-wave")
	waitForScheduledMemoryRelease(t, observed, 2)
	if got := observed.Len(); got != 2 {
		t.Fatalf("rearmed release logs = %d, want 2", got)
	}
	if got := observed.All()[1].ContextMap()["reason"]; got != "second-wave" {
		t.Fatalf("second release reason = %v, want second-wave", got)
	}
}
