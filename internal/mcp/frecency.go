package mcp

import (
	"math"
	"sync"
	"time"

	"github.com/zzet/gortex/internal/persistence"
)

// AgentMode selects the decay regime for frecency + combo. AI sessions
// churn through symbols in minutes, not days, so recent accesses should
// dominate much more aggressively than in a human-editor setting.
type AgentMode int

const (
	// ModeAI is the default for MCP server runs — 3-day half-life.
	ModeAI AgentMode = iota
	// ModeHuman is reserved for interactive CLI / editor integrations
	// where sessions span days or weeks.
	ModeHuman
)

// Exponential half-life constants, chosen so exp(-k * halfLife) = 0.5.
// k = ln(2) / halfLife_in_days.
const (
	aiHalfLifeDays    = 3.0
	humanHalfLifeDays = 10.0
	secondsPerDay     = 86400.0

	// maxFrecencyBoost caps the multiplier applied to a single result so
	// frecency can never override a strong BM25 + combo signal on its own.
	maxFrecencyBoost = 1.5
)

// frecencyTracker records implicit "this symbol was useful" signals by
// timestamping every consume call and exposing a decayed score. Persists
// per repo alongside feedback + combo.
type frecencyTracker struct {
	mu    sync.Mutex
	store persistence.FrecencyStore
	dir   string
	mode  AgentMode

	now func() int64 // seconds since unix epoch; overridable for tests
}

func newFrecencyTracker(cacheDir, repoPath string, mode AgentMode) *frecencyTracker {
	nowFn := func() int64 { return time.Now().Unix() }
	if cacheDir == "" || repoPath == "" {
		return &frecencyTracker{mode: mode, now: nowFn}
	}
	dir := persistence.FrecencyDir(cacheDir, repoPath)
	ft := &frecencyTracker{dir: dir, mode: mode, now: nowFn}
	if loaded, err := persistence.LoadFrecency(dir); err == nil && loaded != nil {
		ft.store = *loaded
	}
	return ft
}

// Record appends one access timestamp for symbolID. The per-symbol history
// is bounded; once the cap is hit the oldest access is dropped.
func (ft *frecencyTracker) Record(symbolID string) {
	if ft == nil || symbolID == "" {
		return
	}
	ft.mu.Lock()
	defer ft.mu.Unlock()

	now := ft.now()
	cap := persistence.MaxFrecencyAccesses()

	idx := ft.findLocked(symbolID)
	if idx < 0 {
		ft.store.Symbols = append(ft.store.Symbols, persistence.FrecencyAccesses{
			SymbolID: symbolID,
			Times:    []int64{now},
		})
	} else {
		s := &ft.store.Symbols[idx]
		s.Times = append(s.Times, now)
		if len(s.Times) > cap {
			s.Times = s.Times[len(s.Times)-cap:]
		}
		ft.moveToEndLocked(idx)
	}

	if ft.dir == "" {
		return
	}
	_ = persistence.SaveFrecency(ft.dir, &ft.store)
}

// Score returns a decayed frecency score for symbolID. Zero when unknown
// or when every access has decayed below the numerical floor.
func (ft *frecencyTracker) Score(symbolID string) float64 {
	if ft == nil || symbolID == "" {
		return 0
	}
	ft.mu.Lock()
	defer ft.mu.Unlock()
	idx := ft.findLocked(symbolID)
	if idx < 0 {
		return 0
	}
	k := ft.decayPerDay()
	now := float64(ft.now())
	var score float64
	for _, t := range ft.store.Symbols[idx].Times {
		dtDays := (now - float64(t)) / secondsPerDay
		if dtDays < 0 {
			dtDays = 0
		}
		score += math.Exp(-k * dtDays)
	}
	return score
}

// BoostFor converts a raw frecency score into a multiplier in [1, maxFrecencyBoost].
// A multiplier of 1 means no reweighting — callers can multiply directly.
func (ft *frecencyTracker) BoostFor(symbolID string) float64 {
	s := ft.Score(symbolID)
	if s <= 0 {
		return 1.0
	}
	// Map a typical recent-access score (1–maxFrecencyAccesses) into
	// [0, maxFrecencyBoost-1]. Saturates quickly so a very hot symbol
	// doesn't steamroll moderately hot ones.
	extra := s / float64(persistence.MaxFrecencyAccesses()) * (maxFrecencyBoost - 1.0)
	if extra > maxFrecencyBoost-1.0 {
		extra = maxFrecencyBoost - 1.0
	}
	return 1.0 + extra
}

// decayPerDay returns k such that the weight halves every halfLife days.
func (ft *frecencyTracker) decayPerDay() float64 {
	halfLife := aiHalfLifeDays
	if ft.mode == ModeHuman {
		halfLife = humanHalfLifeDays
	}
	return math.Ln2 / halfLife
}

func (ft *frecencyTracker) findLocked(id string) int {
	for i := range ft.store.Symbols {
		if ft.store.Symbols[i].SymbolID == id {
			return i
		}
	}
	return -1
}

func (ft *frecencyTracker) moveToEndLocked(idx int) {
	if idx < 0 || idx >= len(ft.store.Symbols)-1 {
		return
	}
	s := ft.store.Symbols[idx]
	copy(ft.store.Symbols[idx:], ft.store.Symbols[idx+1:])
	ft.store.Symbols[len(ft.store.Symbols)-1] = s
}

// HasData reports whether any accesses have been recorded.
func (ft *frecencyTracker) HasData() bool {
	if ft == nil {
		return false
	}
	ft.mu.Lock()
	defer ft.mu.Unlock()
	return len(ft.store.Symbols) > 0
}
