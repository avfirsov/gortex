package telemetry

import (
	"sync"
	"time"
)

// recorderFlushThreshold bounds in-memory accumulation: once this many events
// are pending the recorder flushes to disk, so a long-lived daemon's buffer
// stays small and a crash loses at most this many counts.
const recorderFlushThreshold = 256

// Recorder accumulates allow-listed usage counts in memory and flushes them to
// a per-day Store. It is the gate every recording path goes through, and it
// upholds the telemetry invariants:
//
//   - Consent-gated: a recorder built from disabled consent records nothing,
//     opens no file, and creates no telemetry directory.
//   - Nil-safe: a nil *Recorder's methods are no-ops, so call sites need no
//     branch (handlers stay clean).
//   - Fail-silent: recording is an in-memory map increment under a short mutex;
//     disk errors on flush are swallowed, never retried, never surfaced.
type Recorder struct {
	enabled bool
	store   *Store
	now     func() time.Time

	mu      sync.Mutex
	day     string
	counts  map[string]int
	pending int
}

// NewRecorder builds a recorder for the resolved consent and store. When
// consent is disabled it returns a non-nil recorder that drops everything, so
// callers never need to special-case the off state.
func NewRecorder(consent Consent, store *Store) *Recorder {
	return &Recorder{
		enabled: consent.Enabled,
		store:   store,
		now:     time.Now,
		counts:  map[string]int{},
	}
}

// Record counts one allow-listed event, optionally qualified by a bucketed
// dimension. No-op when the recorder is nil, disabled, has no store, or the key
// is not allow-listed. Never performs I/O on the caller's path beyond an
// occasional threshold flush.
func (r *Recorder) Record(key, dim string) {
	if r == nil || !r.enabled || r.store == nil {
		return
	}
	name, ok := metricName(key, dim)
	if !ok {
		return
	}
	day := DayKey(r.now())

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.day != "" && r.day != day {
		// The process crossed a UTC midnight: persist the prior day before
		// switching so a long-running daemon doesn't smear two days together.
		r.flushLocked()
	}
	r.day = day
	r.counts[name]++
	r.pending++
	if r.pending >= recorderFlushThreshold {
		r.flushLocked()
	}
}

// Flush persists the accumulated counts and resets the in-memory buffer. Call
// it on shutdown (and periodically). No-op when disabled or empty; a store
// error is swallowed — telemetry must never disrupt the process.
func (r *Recorder) Flush() {
	if r == nil || !r.enabled || r.store == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.flushLocked()
}

// Enabled reports whether this recorder will record anything.
func (r *Recorder) Enabled() bool { return r != nil && r.enabled }

// flushLocked merges the pending counts into the day's store file and clears
// the buffer. The caller must hold r.mu.
func (r *Recorder) flushLocked() {
	if r.day == "" || len(r.counts) == 0 {
		r.pending = 0
		return
	}
	roll := &Rollup{Day: r.day, Counts: r.counts}
	_ = r.store.Merge(roll) // fail-silent: a disk error drops these counts
	r.counts = map[string]int{}
	r.pending = 0
}
