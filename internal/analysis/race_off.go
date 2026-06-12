//go:build !race

package analysis

// raceEnabled is true when the binary was built with the Go race
// detector (-race). Race instrumentation adds per-memory-op overhead
// that dominates short timed loops and compresses any precomputed-vs-
// live speedup ratio toward 1.0, so perf-gate tests skip themselves
// when this is true.
const raceEnabled = false
