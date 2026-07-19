package lsp

import (
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// Failure-streak breaker for LSP enrichment phases.
//
// The expensive failure mode this guards is a server that cannot work for a
// workspace at all — wrong project root, no reachable project configuration,
// a wedged process — where every request costs a full timeout and the pass
// grinds through thousands of targets to produce nothing (observed: a 10.7
// minute typescript sweep with zero confirmations). A healthy server that
// errors on individual files answers fast and eventually succeeds somewhere;
// a slow-warming server answers late but then answers. Both are safe here:
// the breaker trips only on an unbroken failure streak with NO successful
// answer ever observed in the phase. After any success it can never trip, so
// it can only abandon work that has demonstrably yielded nothing.
const defaultLSPPhaseFailureStreak = 32

// lspPhaseFailureStreakLimit reads the operator override:
// GORTEX_LSP_BREAKER=0 (or "off"/"false") disables the breaker, a positive
// integer replaces the streak limit.
func lspPhaseFailureStreakLimit() int {
	v := strings.TrimSpace(os.Getenv("GORTEX_LSP_BREAKER"))
	if v == "" {
		return defaultLSPPhaseFailureStreak
	}
	if v == "0" || strings.EqualFold(v, "off") || strings.EqualFold(v, "false") {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return n
	}
	return defaultLSPPhaseFailureStreak
}

type phaseBreaker struct {
	limit   int64
	logger  *zap.Logger
	phase   string
	repo    string
	fails   atomic.Int64
	everOK  atomic.Bool
	tripped atomic.Bool
}

// newPhaseBreaker returns a breaker for one enrichment phase. limit <= 0
// builds a breaker that never trips.
func newPhaseBreaker(limit int, logger *zap.Logger, phase, repo string) *phaseBreaker {
	return &phaseBreaker{limit: int64(limit), logger: logger, phase: phase, repo: repo}
}

// observe records one server interaction. Any success permanently disarms the
// breaker for this phase; failures only count while no success has ever been
// seen. Safe for concurrent use from the phase's worker goroutines.
func (b *phaseBreaker) observe(success bool) {
	if b == nil || b.limit <= 0 || b.tripped.Load() {
		return
	}
	if success {
		b.everOK.Store(true)
		b.fails.Store(0)
		return
	}
	if b.everOK.Load() {
		return
	}
	if b.fails.Add(1) >= b.limit && !b.everOK.Load() {
		if b.tripped.CompareAndSwap(false, true) && b.logger != nil {
			b.logger.Warn("LSP enrich: phase abandoned by zero-yield breaker",
				zap.String("phase", b.phase),
				zap.String("repo", b.repo),
				zap.Int64("consecutive_failures", b.limit),
				zap.String("hint", "server answered no request for this workspace; check its project configuration"))
		}
	}
}

func (b *phaseBreaker) isTripped() bool {
	return b != nil && b.tripped.Load()
}

// Productivity checkpoint (see EnrichRepoContext). The zero-yield breaker
// above catches a server that ERRORS on everything; this complements it for
// a server that ANSWERS everything and resolves nothing for the workspace —
// requests flow, budget burns, yield stays zero.
const (
	defaultLSPProductivityWindow = 120 * time.Second
	// lspProductivityMinRequests is the request volume that must have flowed
	// before a low-yield pass may be cut: a warming server whose requests
	// are blocked (jdtls indexing) never reaches it.
	lspProductivityMinRequests = 100
	// lspProductivityMinYieldPerWindow is the cumulative useful-yield floor
	// (confirms + adds + type stamps) the pass must sustain per elapsed
	// window. Productive passes clear it by orders of magnitude; the
	// dribbling pathology (one stamp per ~30s of timeout-priced requests)
	// stays far below it.
	lspProductivityMinYieldPerWindow = 10
)

// lspProductivityWindow reads the operator override:
// GORTEX_LSP_PRODUCTIVITY_WINDOW=0/"off" disables the checkpoint, a Go
// duration replaces the default window.
func lspProductivityWindow() time.Duration {
	v := strings.TrimSpace(os.Getenv("GORTEX_LSP_PRODUCTIVITY_WINDOW"))
	if v == "" {
		return defaultLSPProductivityWindow
	}
	if v == "0" || strings.EqualFold(v, "off") || strings.EqualFold(v, "false") {
		return 0
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	return defaultLSPProductivityWindow
}

// total sums the issued-request counters — the checkpoint's evidence that
// the server is consuming volume rather than blocking on warmup.
func (s *requestStats) total() int64 {
	return s.references.Load() + s.implementations.Load() + s.definitions.Load() +
		s.hovers.Load() + s.prepareCallHierarchy.Load() + s.outgoingCalls.Load() +
		s.incomingCalls.Load() + s.prepareTypeHierarchy.Load() +
		s.supertypes.Load() + s.subtypes.Load()
}
