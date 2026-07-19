package indexer

import (
	"runtime/debug"
	"testing"
)

func TestRaisedIndexMemoryBudget(t *testing.T) {
	cases := []struct {
		name                string
		calculated, current int64
		want                int64
	}{
		{"no budget derivable", 0, 4 << 30, 0},
		{"negative budget", -1, 4 << 30, 0},
		{"unlimited current takes budget", 8 << 30, 0, 8 << 30},
		{"raise above lower current", 8 << 30, 4 << 30, 8 << 30},
		{"equal current untouched", 4 << 30, 4 << 30, 0},
		{"higher current never lowered", 4 << 30, 6 << 30, 0},
	}
	for _, tc := range cases {
		if got := raisedIndexMemoryBudget(tc.calculated, tc.current); got != tc.want {
			t.Errorf("%s: raisedIndexMemoryBudget(%d, %d) = %d, want %d",
				tc.name, tc.calculated, tc.current, got, tc.want)
		}
	}
}

// The raise policy is process-global runtime state; both subtests mutate and
// exactly restore GC percent and the memory limit, so this test must not run
// in parallel with anything else touching them.
func TestApplyIndexGCTuningMemoryLimitRaisePolicy(t *testing.T) {
	budget := indexMemoryBudget(hostPhysicalMemory(), cgroupMemoryLimit())
	if budget <= 1<<30 {
		t.Skipf("host budget %d too small to observe a raise above 1GiB", budget)
	}
	standing := int64(1) << 30 // a default-policy-sized standing limit below the window budget

	t.Run("default policy limit is raised for the window and restored", func(t *testing.T) {
		prev := debug.SetMemoryLimit(standing)
		defer debug.SetMemoryLimit(prev)
		SetColdIndexMemoryLimitRaise(true)
		defer SetColdIndexMemoryLimitRaise(false)

		restore := applyIndexGCTuning(nil)
		inWindow := debug.SetMemoryLimit(-1)
		restore()
		after := debug.SetMemoryLimit(-1)

		if inWindow != budget {
			t.Errorf("cold window limit = %d, want raised budget %d", inWindow, budget)
		}
		if after != standing {
			t.Errorf("restored limit = %d, want standing %d", after, standing)
		}
	})

	t.Run("explicit operator limit is never raised", func(t *testing.T) {
		prev := debug.SetMemoryLimit(standing)
		defer debug.SetMemoryLimit(prev)
		SetColdIndexMemoryLimitRaise(false)

		restore := applyIndexGCTuning(nil)
		inWindow := debug.SetMemoryLimit(-1)
		restore()
		after := debug.SetMemoryLimit(-1)

		if inWindow != standing {
			t.Errorf("cold window limit = %d, want untouched standing %d", inWindow, standing)
		}
		if after != standing {
			t.Errorf("restored limit = %d, want standing %d", after, standing)
		}
	})
}
