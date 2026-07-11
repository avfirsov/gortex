package main

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/platform"
)

// Daemon memory envelope: a standing soft memory limit installed at boot,
// and a forced heap-to-OS release fired at allocation-burst boundaries.
//
// The daemon is a long-lived background service that shares a developer's
// machine. With the runtime default (no memory limit, GOGC=100) the Go GC
// lets the heap high-water climb toward machine RAM during a burst and then
// keeps that footprint resident — the observed failure was a multi-GB peak
// from a warmup / whole-graph-analysis burst pinning the process footprint
// for hours at idle. A soft memory limit makes the collector pace against a
// ceiling and resist that balloon growth; the release helper returns a
// burst's high-water to the OS so the idle footprint tracks the working set
// rather than the peak. Both are policy, not hard guarantees, and both carry
// env kill-switches so an operator can bypass them entirely.

const (
	// standingMemLimitDivisor takes a quarter of host RAM as the default
	// budget: a background service should leave the bulk of RAM to the
	// editor, compiler, and the app under test.
	standingMemLimitDivisor = 4
	// standingMemLimitFloor is the lowest limit the default policy will
	// set. Below ~1 GiB a daemon with a resident graph would sit in
	// near-constant GC, so the floor trades a little footprint for
	// steady-state responsiveness.
	standingMemLimitFloor = int64(1) << 30 // 1 GiB
	// standingMemLimitCeil caps the default so a large workstation's
	// quarter-RAM figure doesn't hand the daemon a tens-of-GiB budget it
	// never needs — the point of the limit is to resist balloon growth,
	// not to permit it. An operator who genuinely wants more sets an
	// explicit value (honored verbatim, below).
	standingMemLimitCeil = int64(8) << 30 // 8 GiB
)

// memLimitResolution is the outcome of the standing-limit policy: the byte
// value to install (0 = install nothing), where it came from (for the log
// line), and an optional warning when a provided value was malformed and
// ignored.
type memLimitResolution struct {
	limit  int64
	source string // "goenv" | "env" | "config" | "default" | "off" | "unavailable"
	warn   string
}

// resolveStandingMemoryLimit is the pure standing-limit policy. Resolution
// order, highest priority first:
//
//  1. GOMEMLIMIT set — the Go runtime already honors it, so we never fight
//     an explicit operator setting: install nothing, source "goenv".
//  2. GORTEX_DAEMON_MEMLIMIT env var.
//  3. the daemon.memory_limit config value.
//  4. the RAM-derived default policy.
//
// An explicit env/config value is honored verbatim (the operator knows
// their machine); only the default is clamped. "off"/"0" at the env or
// config layer disables the standing limit outright. A malformed env/config
// value is ignored and the decision falls through to the default with a
// warning. Pure — every input is a parameter — so the whole precedence
// table is exhaustively testable without touching the process or the host.
func resolveStandingMemoryLimit(hostRAM uint64, goenv, env, cfg string) memLimitResolution {
	if strings.TrimSpace(goenv) != "" {
		return memLimitResolution{source: "goenv"}
	}
	for _, layer := range []struct{ name, val string }{
		{"env", env},
		{"config", cfg},
	} {
		v := strings.TrimSpace(layer.val)
		if v == "" {
			continue
		}
		n, err := parseByteSize(v)
		if err != nil {
			// A typo'd operator value should not abort boot; apply the safe
			// default instead and surface why. Deliberately does not fall
			// through to the next layer — a malformed value is a signal, not
			// an invitation to keep guessing.
			d := defaultMemLimitResolution(hostRAM)
			d.warn = fmt.Sprintf("invalid %s memory limit %q: %v", layer.name, layer.val, err)
			return d
		}
		if n <= 0 {
			return memLimitResolution{source: "off"}
		}
		return memLimitResolution{limit: n, source: layer.name}
	}
	return defaultMemLimitResolution(hostRAM)
}

// defaultMemLimitResolution wraps the RAM-derived default. Host RAM of 0
// means "unknown" (no portable reader on this platform, or the syscall
// failed): with no machine to reason about, installing an arbitrary limit
// could throttle a large server or over-commit a tiny one, so the safe
// answer is to install nothing.
func defaultMemLimitResolution(hostRAM uint64) memLimitResolution {
	n := defaultStandingMemoryLimit(hostRAM)
	if n <= 0 {
		return memLimitResolution{source: "unavailable"}
	}
	return memLimitResolution{limit: n, source: "default"}
}

// defaultStandingMemoryLimit derives the default soft limit from host RAM:
// a quarter of it, clamped to [floor, ceil]. Returns 0 when host RAM is
// unknown. Pure so the clamps are table-testable without a host.
func defaultStandingMemoryLimit(hostRAM uint64) int64 {
	if hostRAM == 0 {
		return 0
	}
	limit := int64(hostRAM / standingMemLimitDivisor)
	if limit < standingMemLimitFloor {
		limit = standingMemLimitFloor
	}
	if limit > standingMemLimitCeil {
		limit = standingMemLimitCeil
	}
	return limit
}

// parseByteSize parses a human byte size into an exact byte count. It
// accepts a bare integer (bytes) or an integer with a binary unit suffix —
// K/KB/KiB, M/MB/MiB, G/GB/GiB, T/TB/TiB — all interpreted as powers of
// 1024, case-insensitively, since this sizes a memory budget. "off", "0",
// and the empty string parse to 0 (the caller reads 0 as "disabled"). A
// malformed number, an unrecognised unit, or a value that would overflow
// int64 returns an error so the caller can fall back to the default policy.
func parseByteSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	low := strings.ToLower(s)
	if low == "off" || low == "0" {
		return 0, nil
	}
	i := 0
	for i < len(low) && low[i] >= '0' && low[i] <= '9' {
		i++
	}
	numPart := low[:i]
	unit := strings.TrimSpace(low[i:])
	if numPart == "" {
		return 0, fmt.Errorf("invalid byte size %q", s)
	}
	n, err := strconv.ParseInt(numPart, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid byte size %q", s)
	}
	var mult int64
	switch unit {
	case "", "b":
		mult = 1
	case "k", "kb", "kib":
		mult = 1 << 10
	case "m", "mb", "mib":
		mult = 1 << 20
	case "g", "gb", "gib":
		mult = 1 << 30
	case "t", "tb", "tib":
		mult = 1 << 40
	default:
		return 0, fmt.Errorf("invalid byte-size unit %q in %q", unit, s)
	}
	if mult > 1 && n > math.MaxInt64/mult {
		return 0, fmt.Errorf("byte size out of range %q", s)
	}
	return n * mult, nil
}

// applyStandingMemoryLimit resolves and installs the daemon's standing soft
// memory limit. Call once at boot, after logging and config are up and
// before warmup starts allocating.
//
// Composition with the cold-index window (internal/indexer/gc_tune.go): a
// cold index briefly raises the limit to a larger budget (RAM/2) and, on
// exit, restores the value it captured via debug.SetMemoryLimit(-1) — which
// is exactly the standing limit installed here. Installing this before any
// index runs is what makes that restore land on our value rather than on
// "no limit". The two are therefore composable: the daemon holds a modest
// standing ceiling, cold indexes get their wider one-shot budget, and the
// standing ceiling comes back afterward untouched.
func applyStandingMemoryLimit(logger *zap.Logger, cfgVal string) {
	d := resolveStandingMemoryLimit(
		platform.HostPhysicalMemoryBytes(),
		os.Getenv("GOMEMLIMIT"),
		os.Getenv("GORTEX_DAEMON_MEMLIMIT"),
		cfgVal,
	)
	if d.warn != "" && logger != nil {
		logger.Warn("daemon: standing memory limit — falling back to default",
			zap.String("reason", d.warn))
	}
	switch d.source {
	case "goenv":
		if logger != nil {
			logger.Info("daemon: standing memory limit deferred to GOMEMLIMIT")
		}
		return
	case "off":
		if logger != nil {
			logger.Info("daemon: standing memory limit disabled by configuration")
		}
		return
	case "unavailable":
		if logger != nil {
			logger.Debug("daemon: standing memory limit skipped — host RAM unknown")
		}
		return
	}
	debug.SetMemoryLimit(d.limit)
	if logger != nil {
		logger.Info("daemon: standing memory limit applied",
			zap.Int64("bytes", d.limit),
			zap.String("source", d.source))
	}
}

// memReleaseEnabled reports whether post-burst heap release is active. On by
// default; GORTEX_DAEMON_MEMRELEASE=0 (or "false") disables it.
func memReleaseEnabled() bool {
	v := os.Getenv("GORTEX_DAEMON_MEMRELEASE")
	return v != "0" && !strings.EqualFold(v, "false")
}

// releaseMemoryToOS forces a GC + scavenge (runtime/debug.FreeOSMemory) so a
// just-completed allocation burst's high-water heap is returned to the OS
// promptly instead of pinning the process footprint at its peak.
//
// It is called only at burst boundaries (warmup end, the reconcile janitor
// after a tick that did work), never on a timer: FreeOSMemory runs a full,
// largely stop-the-world GC cycle costing ~0.1–2 s on a multi-GB heap, so
// paying it once per burst is fine while paying it periodically would
// reintroduce exactly the steady-state GC cost the standing limit is tuned
// to avoid. HeapReleased is monotonic across the forced scavenge, so the
// logged delta is the bytes this call handed back.
//
// GORTEX_DAEMON_MEMRELEASE=0 (or "false") turns it into a no-op.
func releaseMemoryToOS(logger *zap.Logger, reason string) {
	if !memReleaseEnabled() {
		return
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	debug.FreeOSMemory()
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	// Concurrent allocation between the two reads can re-acquire released
	// pages faster than this call released them, making the raw delta
	// negative; report that as zero net release rather than a nonsense
	// negative byte count.
	freed := int64(after.HeapReleased) - int64(before.HeapReleased)
	if freed < 0 {
		freed = 0
	}
	if logger != nil {
		logger.Info("daemon: released heap to OS",
			zap.String("reason", reason),
			zap.Duration("elapsed", elapsed),
			zap.Int64("freed_bytes", freed),
			zap.Uint64("heap_sys_bytes", after.HeapSys),
			zap.Uint64("heap_released_bytes", after.HeapReleased))
	}
}
