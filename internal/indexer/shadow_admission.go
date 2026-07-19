package indexer

import (
	"os"
	"strconv"
	"sync"
)

const (
	// Keep aggregate cold-start shadow state comfortably below the resident
	// database working set on a typical development machine. Operators can
	// override the process-wide ceiling before daemon start.
	defaultShadowProcessBudgetBytes int64 = 1 << 30 // 1 GiB

	// A source file expands into nodes, edges, indexes, and drain buffers. Raw
	// input bytes alone badly undercount source-heavy repositories, so admission
	// charges both input expansion and a per-file structural estimate.
	shadowInputExpansion        int64 = 2
	shadowEstimatedBytesPerFile int64 = 128 << 10
	shadowMinimumChargeBytes    int64 = 32 << 20
)

// shadowAdmissionBudget is a non-blocking, weighted process admission gate.
// Repositories that do not fit immediately use SQLite directly; they never wait
// behind another in-memory shadow and never create an unbudgeted fallback.
type shadowAdmissionBudget struct {
	mu       sync.Mutex
	capacity int64
	used     int64
	peak     int64
}

type shadowAdmissionLease struct {
	budget *shadowAdmissionBudget
	weight int64
	once   sync.Once
}

var processShadowAdmission = newShadowAdmissionBudget(shadowProcessBudgetBytes())

func newShadowAdmissionBudget(capacity int64) *shadowAdmissionBudget {
	if capacity < 0 {
		capacity = 0
	}
	return &shadowAdmissionBudget{capacity: capacity}
}

// shadowProcessBudgetBytes returns the process-wide in-memory shadow budget.
// Zero explicitly disables shadows. Invalid or negative values use the safe
// default. The environment is read once when the process gate is constructed.
func shadowProcessBudgetBytes() int64 {
	if raw := os.Getenv("GORTEX_SHADOW_BUDGET_BYTES"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err == nil && value >= 0 {
			return value
		}
	}
	return defaultShadowProcessBudgetBytes
}

func shadowAdmissionWeight(fileCount int, inputBytes int64) int64 {
	if fileCount < 0 {
		fileCount = 0
	}
	if inputBytes < 0 {
		inputBytes = 0
	}
	// Saturate instead of overflowing an admission charge into a small value.
	const maxInt64 = int64(^uint64(0) >> 1)
	weight := inputBytes
	if inputBytes > maxInt64/shadowInputExpansion {
		weight = maxInt64
	} else {
		weight *= shadowInputExpansion
	}
	fileWeight := int64(fileCount)
	if fileWeight > maxInt64/shadowEstimatedBytesPerFile {
		fileWeight = maxInt64
	} else {
		fileWeight *= shadowEstimatedBytesPerFile
	}
	if weight > maxInt64-fileWeight {
		weight = maxInt64
	} else {
		weight += fileWeight
	}
	if weight < shadowMinimumChargeBytes {
		weight = shadowMinimumChargeBytes
	}
	return weight
}

func (b *shadowAdmissionBudget) tryAcquire(weight int64) (*shadowAdmissionLease, bool) {
	if b == nil || weight <= 0 {
		return nil, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.capacity <= 0 || weight > b.capacity-b.used {
		return nil, false
	}
	b.used += weight
	if b.used > b.peak {
		b.peak = b.used
	}
	return &shadowAdmissionLease{budget: b, weight: weight}, true
}

func (l *shadowAdmissionLease) Release() {
	if l == nil || l.budget == nil {
		return
	}
	l.once.Do(func() {
		l.budget.mu.Lock()
		l.budget.used -= l.weight
		if l.budget.used < 0 {
			l.budget.used = 0
		}
		l.budget.mu.Unlock()
	})
}

func (b *shadowAdmissionBudget) snapshot() (capacity, used, peak int64) {
	if b == nil {
		return 0, 0, 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.capacity, b.used, b.peak
}
