package main

import (
	"runtime"
	"testing"

	"go.uber.org/zap"
)

const (
	kib = int64(1) << 10
	mib = int64(1) << 20
	gib = int64(1) << 30
	tib = int64(1) << 40
)

func TestParseByteSize(t *testing.T) {
	tests := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"", 0, false},
		{"0", 0, false},
		{"off", 0, false},
		{"OFF", 0, false},
		{" off ", 0, false},
		{"1024", 1024, false}, // bare number = bytes
		{"1B", 1, false},
		{"1KiB", kib, false},
		{"1kb", kib, false}, // KB treated as binary (memory budget)
		{"2K", 2 * kib, false},
		{"4096MiB", 4096 * mib, false}, // == 4 GiB
		{"4GiB", 4 * gib, false},
		{"2G", 2 * gib, false},
		{"2gb", 2 * gib, false},
		{"1T", tib, false},
		{"1TiB", tib, false},
		{"nonsense", 0, true},
		{"12x", 0, true},                   // unknown unit
		{"GiB", 0, true},                   // no number
		{"-5GiB", 0, true},                 // negative
		{"99999999999999999999G", 0, true}, // overflow
	}
	for _, tc := range tests {
		got, err := parseByteSize(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseByteSize(%q): expected error, got %d", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseByteSize(%q): unexpected error %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseByteSize(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestDefaultStandingMemoryLimit(t *testing.T) {
	tests := []struct {
		name    string
		hostRAM uint64
		want    int64
	}{
		{"unknown host RAM => 0", 0, 0},
		{"tiny host clamps up to floor", uint64(2) * uint64(gib), standingMemLimitFloor},   // 2GiB/4=512MiB -> 1GiB
		{"mid host uses quarter", uint64(16) * uint64(gib), 4 * gib},                       // 16GiB/4=4GiB
		{"large host clamps down to ceil", uint64(64) * uint64(gib), standingMemLimitCeil}, // 64GiB/4=16GiB -> 8GiB
		{"exactly floor boundary", uint64(4) * uint64(gib), gib},                           // 4GiB/4=1GiB
		{"exactly ceil boundary", uint64(32) * uint64(gib), 8 * gib},                       // 32GiB/4=8GiB
	}
	for _, tc := range tests {
		if got := defaultStandingMemoryLimit(tc.hostRAM); got != tc.want {
			t.Errorf("%s: defaultStandingMemoryLimit(%d) = %d, want %d", tc.name, tc.hostRAM, got, tc.want)
		}
	}
}

func TestResolveStandingMemoryLimit(t *testing.T) {
	const host = uint64(16) * uint64(1<<30) // 16 GiB -> default 4 GiB
	tests := []struct {
		name       string
		hostRAM    uint64
		goenv      string
		env        string
		cfg        string
		wantLimit  int64
		wantSource string
		wantWarn   bool
	}{
		{"GOMEMLIMIT wins, install nothing", host, "5GiB", "4GiB", "2GiB", 0, "goenv", false},
		{"GOMEMLIMIT wins even when malformed-looking", host, "someval", "4GiB", "", 0, "goenv", false},
		{"env value honored verbatim", host, "", "6GiB", "2GiB", 6 * gib, "env", false},
		{"env off disables", host, "", "off", "2GiB", 0, "off", false},
		{"env zero disables", host, "", "0", "2GiB", 0, "off", false},
		{"config used when env empty", host, "", "", "3GiB", 3 * gib, "config", false},
		{"config off disables", host, "", "", "off", 0, "off", false},
		{"default when nothing set", host, "", "", "", 4 * gib, "default", false},
		{"malformed env falls back to default with warning", host, "", "bogus", "2GiB", 4 * gib, "default", true},
		{"malformed config falls back to default with warning", host, "", "", "bogus", 4 * gib, "default", true},
		{"default clamps up on tiny host", uint64(2) * uint64(gib), "", "", "", standingMemLimitFloor, "default", false},
		{"default clamps down on large host", uint64(128) * uint64(gib), "", "", "", standingMemLimitCeil, "default", false},
		{"unknown host and no explicit value", 0, "", "", "", 0, "unavailable", false},
	}
	for _, tc := range tests {
		got := resolveStandingMemoryLimit(tc.hostRAM, tc.goenv, tc.env, tc.cfg)
		if got.limit != tc.wantLimit || got.source != tc.wantSource {
			t.Errorf("%s: resolve(...) = {limit:%d, source:%q}, want {limit:%d, source:%q}",
				tc.name, got.limit, got.source, tc.wantLimit, tc.wantSource)
		}
		if (got.warn != "") != tc.wantWarn {
			t.Errorf("%s: warn=%q, wantWarn=%v", tc.name, got.warn, tc.wantWarn)
		}
	}
}

func TestMemReleaseEnabled(t *testing.T) {
	tests := []struct {
		val  string
		want bool
	}{
		{"", true}, // unset => on by default
		{"1", true},
		{"true", true},
		{"anything", true},
		{"0", false}, // kill-switch
		{"false", false},
		{"FALSE", false},
	}
	for _, tc := range tests {
		t.Setenv("GORTEX_DAEMON_MEMRELEASE", tc.val)
		if got := memReleaseEnabled(); got != tc.want {
			t.Errorf("memReleaseEnabled() with %q = %v, want %v", tc.val, got, tc.want)
		}
	}
}

func TestReleaseMemoryToOS_KillSwitch(t *testing.T) {
	// With the kill-switch set, the helper must be a no-op: it must not
	// panic and must return without forcing a collection. We can only
	// observe "no crash / returns" here; the enablement predicate is
	// covered exhaustively by TestMemReleaseEnabled.
	t.Setenv("GORTEX_DAEMON_MEMRELEASE", "0")
	releaseMemoryToOS(zap.NewNop(), "kill-switch-test")
}

func TestReleaseMemoryToOS_Smoke(t *testing.T) {
	t.Setenv("GORTEX_DAEMON_MEMRELEASE", "1")

	// Allocate then drop a chunk so there is heap for the forced scavenge
	// to return, then confirm HeapReleased did not go backwards across the
	// call. Exact byte counts are intentionally not asserted.
	sink := make([]byte, 8<<20)
	for i := range sink {
		sink[i] = byte(i)
	}
	// Force the writes to land (so the allocation is not elided), then let the
	// reference drop here: sink is dead past this point, so the scavenge below
	// has heap to return.
	runtime.KeepAlive(sink)

	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	releaseMemoryToOS(zap.NewNop(), "smoke-test")

	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	if after.HeapReleased < before.HeapReleased {
		t.Fatalf("HeapReleased went backwards after FreeOSMemory: before=%d after=%d",
			before.HeapReleased, after.HeapReleased)
	}
}
