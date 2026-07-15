package mcp

import (
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/runtimeactivity"
)

const (
	defaultIdleReleaseMinBytes = 128 << 20
	defaultIdleReleaseDelay    = 100 * time.Millisecond
	defaultIdleReleaseQuiet    = 3 * time.Second
	minimumIdleReleaseQuiet    = 2 * time.Second
	maximumIdleReleaseQuiet    = 5 * time.Second
	defaultIdleReleaseCooldown = 15 * time.Second
)

var lastMemoryReleaseAtNanos atomic.Int64

// MCP calls participate in the process-wide activity tracker. The same tracker
// also covers warmup, reconciliation, snapshots, analysis, and other background
// work, because debug.FreeOSMemory is process-wide and must not overlap any of
// them.
func beginMCPToolCall() {
	runtimeactivity.Begin("mcp")
}

func endMCPToolCall(logger *zap.Logger, tool string) {
	runtimeactivity.End("mcp")
	scheduleOSMemoryReleaseAfterBurst(logger, "mcp_tool:"+tool)
}

func memoryReleaseEnabled() bool {
	v := strings.TrimSpace(os.Getenv("GORTEX_DAEMON_MEMRELEASE"))
	return v != "0" && !strings.EqualFold(v, "false")
}

func idleReleaseMinBytes() uint64 {
	return envMegabytes("GORTEX_DAEMON_MEMRELEASE_MIN_MB", defaultIdleReleaseMinBytes)
}

func idleReleaseCooldown() time.Duration {
	v := strings.TrimSpace(os.Getenv("GORTEX_DAEMON_MEMRELEASE_COOLDOWN"))
	if v == "" {
		return defaultIdleReleaseCooldown
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return defaultIdleReleaseCooldown
	}
	return d
}

// idleReleaseQuiet is deliberately bounded. Shorter windows race normal tool
// alternation and turn reclamation into user-visible latency; much longer ones
// strand a burst's high-water in otherwise quiet daemon sessions.
func idleReleaseQuiet() time.Duration {
	v := strings.TrimSpace(os.Getenv("GORTEX_DAEMON_MEMRELEASE_QUIET"))
	if v == "" {
		return defaultIdleReleaseQuiet
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return defaultIdleReleaseQuiet
	}
	if d < minimumIdleReleaseQuiet {
		return minimumIdleReleaseQuiet
	}
	if d > maximumIdleReleaseQuiet {
		return maximumIdleReleaseQuiet
	}
	return d
}

func envMegabytes(name string, fallback uint64) uint64 {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	mb, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return fallback
	}
	const mib = uint64(1 << 20)
	if mb > ^uint64(0)/mib {
		return fallback
	}
	return mb * mib
}

func heapIdleUnreleased(m *runtime.MemStats) uint64 {
	if m == nil || m.HeapIdle <= m.HeapReleased {
		return 0
	}
	return m.HeapIdle - m.HeapReleased
}

// releaseIdleMCPHeap is the zero-quiet test/benchmark entry point. Production
// scheduling uses releaseIdleHeapAfterQuiet with the bounded quiet window.
func releaseIdleMCPHeap(logger *zap.Logger, reason string) (done bool, retryAfter time.Duration) {
	return releaseIdleHeapAfterQuiet(logger, reason, 0)
}

// releaseIdleHeapAfterQuiet performs one adaptive release attempt. The
// process-wide tracker closes the check-to-release race: once admitted, no MCP
// request or tracked background job can begin until the scavenge completes.
func releaseIdleHeapAfterQuiet(logger *zap.Logger, reason string, quiet time.Duration) (done bool, retryAfter time.Duration) {
	var (
		attemptDone  bool
		attemptRetry time.Duration
	)
	ran, trackerRetry := runtimeactivity.RunIfQuiet(quiet, func() {
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		candidate := heapIdleUnreleased(&before)
		if candidate < idleReleaseMinBytes() {
			attemptDone = true
			return
		}

		if cooldown := idleReleaseCooldown(); cooldown > 0 {
			last := time.Unix(0, lastMemoryReleaseAtNanos.Load())
			if remaining := cooldown - time.Since(last); remaining > 0 {
				attemptRetry = remaining
				return
			}
		}

		start := time.Now()
		debug.FreeOSMemory()
		lastMemoryReleaseAtNanos.Store(time.Now().UnixNano())

		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		if logger != nil {
			logger.Debug("daemon: released idle heap to OS",
				zap.String("reason", reason),
				zap.Uint64("heap_alloc_bytes", after.HeapAlloc),
				zap.Uint64("heap_inuse_bytes", after.HeapInuse),
				zap.Uint64("heap_idle_bytes", after.HeapIdle),
				zap.Uint64("heap_idle_unreleased_before_bytes", candidate),
				zap.Uint64("heap_idle_unreleased_after_bytes", heapIdleUnreleased(&after)),
				zap.Uint64("heap_released_before_bytes", before.HeapReleased),
				zap.Uint64("heap_released_after_bytes", after.HeapReleased),
				zap.Uint64("heap_sys_bytes", after.HeapSys),
				zap.Uint64("stack_inuse_bytes", after.StackInuse),
				zap.Duration("elapsed", time.Since(start)))
		}
		attemptDone = true
	})
	if !ran {
		if trackerRetry <= 0 {
			trackerRetry = defaultIdleReleaseDelay
		}
		return false, trackerRetry
	}
	if attemptRetry > 0 {
		return false, attemptRetry
	}
	return attemptDone, 0
}

// runScheduledMCPMemoryRelease debounces all tracked process activity into a
// real quiet window, then releases only material idle, unreleased heap. Epoch
// checks make the scheduler lost-wakeup safe when work starts or ends while it
// is deciding whether to disarm.
func runScheduledMCPMemoryRelease(logger *zap.Logger, reason string) {
	seen := runtimeactivity.Current().Epoch
	for {
		time.Sleep(defaultIdleReleaseDelay)
		snapshot := runtimeactivity.Current()
		if snapshot.Active != 0 || snapshot.Epoch != seen {
			seen = snapshot.Epoch
			continue
		}

		done, retryAfter := releaseIdleHeapAfterQuiet(logger, reason, idleReleaseQuiet())
		if !done {
			if retryAfter > defaultIdleReleaseDelay {
				time.Sleep(retryAfter - defaultIdleReleaseDelay)
			}
			seen = runtimeactivity.Current().Epoch
			continue
		}

		osMemoryReleaseScheduled.Store(false)
		current := runtimeactivity.Current()
		if current.Active == 0 && current.Epoch != snapshot.Epoch {
			scheduleOSMemoryReleaseAfterBurst(logger, reason)
		}
		return
	}
}
