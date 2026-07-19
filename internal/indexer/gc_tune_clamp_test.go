package indexer

import (
	"math"
	"runtime/debug"
	"testing"
)

func TestShouldRaiseGCPercent(t *testing.T) {
	cases := []struct {
		name                  string
		calculated, installed int64
		want                  bool
	}{
		{"no limit installed", 8 << 30, 0, true},
		{"no budget derivable under a limit", 0, 2 << 30, false},
		{"limit clamped below window budget", 12 << 30, 2 << 30, false},
		{"limit meets window budget", 12 << 30, 12 << 30, true},
		{"limit above window budget", 12 << 30, 16 << 30, true},
	}
	for _, tc := range cases {
		if got := shouldRaiseGCPercent(tc.calculated, tc.installed); got != tc.want {
			t.Errorf("%s: shouldRaiseGCPercent(%d, %d) = %v, want %v",
				tc.name, tc.calculated, tc.installed, got, tc.want)
		}
	}
}

func TestForcedHeapReleaseNeeded(t *testing.T) {
	cases := []struct {
		name             string
		heapInuse, limit int64
		want             bool
	}{
		{"no finite limit", 4 << 30, math.MaxInt64, false},
		{"no limit installed", 4 << 30, 0, false},
		{"below half the ceiling", 1 << 30, 4 << 30, false},
		{"above half the ceiling", 3 << 30, 4 << 30, true},
	}
	for _, tc := range cases {
		if got := forcedHeapReleaseNeeded(tc.heapInuse, tc.limit); got != tc.want {
			t.Errorf("%s: forcedHeapReleaseNeeded(%d, %d) = %v, want %v",
				tc.name, tc.heapInuse, tc.limit, got, tc.want)
		}
	}
}

// A window whose memory limit stays clamped below its own calculated budget
// must leave the GC percent at the runtime's prior value: relaxed pacing
// under a tight ceiling hands collection to the soft limit and the burst
// degrades into assist storms.
func TestApplyIndexGCTuningKeepsGCPercentWhenClamped(t *testing.T) {
	budget := indexMemoryBudget(hostPhysicalMemory(), cgroupMemoryLimit())
	if budget <= 0 {
		t.Skip("no usable host budget on this machine")
	}
	standing := budget / 4
	if standing <= 0 {
		t.Skip("host budget too small to derive a clamped standing limit")
	}

	prevLim := debug.SetMemoryLimit(standing)
	defer debug.SetMemoryLimit(prevLim)
	SetColdIndexMemoryLimitRaise(false)

	prevPct := debug.SetGCPercent(100)
	defer debug.SetGCPercent(prevPct)

	restore := applyIndexGCTuning(nil)
	inWindowPct := debug.SetGCPercent(100)
	debug.SetGCPercent(inWindowPct)
	restore()
	afterPct := debug.SetGCPercent(100)
	debug.SetGCPercent(afterPct)

	if inWindowPct != 100 {
		t.Errorf("clamped window GC percent = %d, want unchanged 100", inWindowPct)
	}
	if afterPct != 100 {
		t.Errorf("restored GC percent = %d, want 100", afterPct)
	}
}

// The mirror case: when the raise policy applies and the window installs its
// own budget, the relaxed GC percent is used inside the window and restored
// exactly on close.
func TestApplyIndexGCTuningRaisesGCPercentWithOwnBudget(t *testing.T) {
	budget := indexMemoryBudget(hostPhysicalMemory(), cgroupMemoryLimit())
	if budget <= 0 {
		t.Skip("no usable host budget on this machine")
	}
	standing := budget / 4
	if standing <= 0 {
		t.Skip("host budget too small to derive a standing limit")
	}

	prevLim := debug.SetMemoryLimit(standing)
	defer debug.SetMemoryLimit(prevLim)
	SetColdIndexMemoryLimitRaise(true)
	defer SetColdIndexMemoryLimitRaise(false)

	prevPct := debug.SetGCPercent(100)
	defer debug.SetGCPercent(prevPct)

	restore := applyIndexGCTuning(nil)
	inWindowPct := debug.SetGCPercent(100)
	debug.SetGCPercent(inWindowPct)
	restore()
	afterPct := debug.SetGCPercent(100)
	debug.SetGCPercent(afterPct)

	if want := indexGCPercent(); inWindowPct != want {
		t.Errorf("raised window GC percent = %d, want %d", inWindowPct, want)
	}
	if afterPct != 100 {
		t.Errorf("restored GC percent = %d, want 100", afterPct)
	}
}
