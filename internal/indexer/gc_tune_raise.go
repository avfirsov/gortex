package indexer

import "sync/atomic"

// Cold-index memory-limit raise.
//
// applyIndexGCTuning derives a host/cgroup budget (indexMemoryBudget) for the
// cold-index window, but boundedIndexMemoryBudget never raises an
// already-lower process limit — the right default when that limit is an
// explicit operator setting (GOMEMLIMIT, GORTEX_DAEMON_MEMLIMIT, config).
//
// The daemon's DEFAULT standing limit is different: it is a steady-state
// policy (a quarter of host RAM, capped) installed by the daemon itself, not
// an operator decision. Pinning the one-shot cold-index burst to that
// steady-state ceiling makes the collector pace against a limit far below the
// window's own budget and burns a large share of the burst's CPU in GC.
// The daemon therefore declares, at boot, whether the standing limit came
// from its own default policy; only then may the cold window raise the limit
// toward the window budget. The prior limit is still restored exactly when
// the window closes, so steady state is untouched.
var coldIndexMemLimitRaise atomic.Bool

// SetColdIndexMemoryLimitRaise declares whether the process's current memory
// limit came from the daemon's own default policy (true) or from an explicit
// operator setting (false — never raise). Called once at daemon boot, before
// warmup starts indexing.
func SetColdIndexMemoryLimitRaise(allowed bool) {
	coldIndexMemLimitRaise.Store(allowed)
}

// ColdIndexMemoryLimitRaiseAllowed reports the declared policy. Exported for
// the daemon boot test; the indexer reads it inside applyIndexGCTuning.
func ColdIndexMemoryLimitRaiseAllowed() bool {
	return coldIndexMemLimitRaise.Load()
}

// raisedIndexMemoryBudget selects the memory limit to install for the
// cold-index window when raising above the current (default-policy) limit is
// allowed. It returns `calculated` only when that is a real budget above the
// current limit; otherwise 0, meaning "leave the current limit untouched".
// The current limit is never lowered here — a window budget below the
// standing limit would silently tighten a policy the operator can already
// tighten explicitly.
func raisedIndexMemoryBudget(calculated, current int64) int64 {
	if calculated <= 0 {
		return 0
	}
	if current > 0 && current >= calculated {
		return 0
	}
	return calculated
}
