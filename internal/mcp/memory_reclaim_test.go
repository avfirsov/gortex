package mcp

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/zzet/gortex/internal/runtimeactivity"
)

func TestHeapIdleUnreleased(t *testing.T) {
	for _, tc := range []struct {
		name string
		idle uint64
		rel  uint64
		want uint64
	}{
		{name: "difference", idle: 9, rel: 4, want: 5},
		{name: "fully released", idle: 9, rel: 9, want: 0},
		{name: "defensive underflow", idle: 4, rel: 9, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := heapIdleUnreleased(&runtime.MemStats{HeapIdle: tc.idle, HeapReleased: tc.rel})
			if got != tc.want {
				t.Fatalf("heapIdleUnreleased() = %d, want %d", got, tc.want)
			}
		})
	}
	if got := heapIdleUnreleased(nil); got != 0 {
		t.Fatalf("heapIdleUnreleased(nil) = %d, want 0", got)
	}
}

func TestReleaseIdleMCPHeapSkipsBelowThreshold(t *testing.T) {
	waitForMemoryReleaseSchedulerIdle(t)
	t.Setenv("GORTEX_DAEMON_MEMRELEASE_MIN_MB", "1048576") // 1 TiB
	t.Setenv("GORTEX_DAEMON_MEMRELEASE_COOLDOWN", "0")

	done, retryAfter := releaseIdleMCPHeap(nil, "below-threshold")
	if !done || retryAfter != 0 {
		t.Fatalf("releaseIdleMCPHeap() = (%v, %v), want (true, 0)", done, retryAfter)
	}
}

func TestReleaseIdleMCPHeapDefersWhileToolActive(t *testing.T) {
	waitForMemoryReleaseSchedulerIdle(t)
	t.Setenv("GORTEX_DAEMON_MEMRELEASE", "false")
	baseline := runtimeactivity.Current().Active

	beginMCPToolCall()
	done, retryAfter := releaseIdleMCPHeap(nil, "active")
	if done || retryAfter <= 0 {
		t.Fatalf("releaseIdleMCPHeap() = (%v, %v), want deferred retry", done, retryAfter)
	}
	if got := runtimeactivity.Current().Active; got != baseline+1 {
		t.Fatalf("active work = %d, want %d", got, baseline+1)
	}
	endMCPToolCall(nil, "active")
	if got := runtimeactivity.Current().Active; got != baseline {
		t.Fatalf("active work after end = %d, want %d", got, baseline)
	}
}

func TestReleaseIdleHeapDefersForBackgroundActivity(t *testing.T) {
	waitForMemoryReleaseSchedulerIdle(t)
	t.Setenv("GORTEX_DAEMON_MEMRELEASE", "false")

	runtimeactivity.Begin("warmup")
	defer runtimeactivity.End("warmup")
	done, retryAfter := releaseIdleMCPHeap(nil, "background-active")
	if done || retryAfter <= 0 {
		t.Fatalf("release during background work = (%v, %v), want deferred retry", done, retryAfter)
	}
}

func TestWrappedToolHandlerBalancesActiveCalls(t *testing.T) {
	waitForMemoryReleaseSchedulerIdle(t)
	t.Setenv("GORTEX_DAEMON_MEMRELEASE", "false")
	baseline := runtimeactivity.Current().Active

	started := make(chan struct{})
	finish := make(chan struct{})
	s := &Server{}
	wrapped := s.wrapControlToolHandler(func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		close(started)
		<-finish
		return mcp.NewToolResultText("ok"), nil
	})
	var req mcp.CallToolRequest
	req.Params.Name = "memory_reclaim_test"

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = wrapped(context.Background(), req)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("wrapped handler did not start")
	}
	if got := runtimeactivity.Current().Active; got != baseline+1 {
		t.Fatalf("active work while handler runs = %d, want %d", got, baseline+1)
	}
	close(finish)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("wrapped handler did not finish")
	}
	if got := runtimeactivity.Current().Active; got != baseline {
		t.Fatalf("active work after handler = %d, want %d", got, baseline)
	}
}

func TestIdleReleaseQuietIsBounded(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  time.Duration
	}{
		{name: "default", value: "", want: defaultIdleReleaseQuiet},
		{name: "minimum", value: "1ms", want: minimumIdleReleaseQuiet},
		{name: "maximum", value: "1h", want: maximumIdleReleaseQuiet},
		{name: "explicit", value: "4s", want: 4 * time.Second},
		{name: "invalid", value: "later", want: defaultIdleReleaseQuiet},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GORTEX_DAEMON_MEMRELEASE_QUIET", tc.value)
			if got := idleReleaseQuiet(); got != tc.want {
				t.Fatalf("idleReleaseQuiet() = %v, want %v", got, tc.want)
			}
		})
	}
}

func BenchmarkAdaptiveIdleRelease(b *testing.B) {
	b.Setenv("GORTEX_DAEMON_MEMRELEASE_MIN_MB", "0")
	b.Setenv("GORTEX_DAEMON_MEMRELEASE_COOLDOWN", "0")

	const burstBytes = 256 << 20
	for range b.N {
		burst := make([]byte, burstBytes)
		for i := 0; i < len(burst); i += 4096 {
			burst[i] = 1
		}
		runtime.KeepAlive(burst)
		runtime.GC()

		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		done, retryAfter := releaseIdleMCPHeap(nil, "benchmark")
		if !done || retryAfter != 0 {
			b.Fatalf("releaseIdleMCPHeap() = (%v, %v)", done, retryAfter)
		}
		var after runtime.MemStats
		runtime.ReadMemStats(&after)

		b.ReportMetric(float64(before.HeapAlloc)/(1<<20), "heap-live-before-MiB")
		b.ReportMetric(float64(after.HeapAlloc)/(1<<20), "heap-live-after-MiB")
		b.ReportMetric(float64(heapIdleUnreleased(&before))/(1<<20), "idle-unreleased-before-MiB")
		b.ReportMetric(float64(heapIdleUnreleased(&after))/(1<<20), "idle-unreleased-after-MiB")
		if after.HeapReleased >= before.HeapReleased {
			b.ReportMetric(float64(after.HeapReleased-before.HeapReleased)/(1<<20), "released-delta-MiB")
		}
	}
}
