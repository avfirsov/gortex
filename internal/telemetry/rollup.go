package telemetry

import (
	"regexp"
	"time"
)

// allowedMetrics is the hard allow-list of telemetry counter keys. It is the
// privacy backbone: the aggregator physically cannot record a key that is not
// on this list, so a path, a symbol name, or any arbitrary string can never
// become a metric. Adding a counter is a deliberate edit here and nowhere else.
var allowedMetrics = map[string]bool{
	"mcp_tool_call":      true, // an MCP tool was invoked; dim = tool name
	"mcp_facade_call":    true, // a facade-v1 attempt; dim = facade.operation
	"mcp_facade_status":  true, // facade result class; dim = facade.operation.ok|error
	"mcp_facade_outcome": true, // bounded facade outcome; dim = facade.operation.outcome
	"mcp_facade_invalid": true, // validation failure; dim = facade.operation.error_code
	"mcp_facade_latency": true, // end-to-end latency; dim = facade.operation.duration_bucket
	"cli_command":        true, // a CLI subcommand ran; dim = command name
	"index":              true, // an index pass completed; dim = file-count bucket
	"index_lang":         true, // a language was present in an index pass; dim = language
	"daemon_session":     true, // a daemon session started; dim = backend kind
	"install":            true, // an install applied; dim = agent target / scope
	"uninstall":          true, // an uninstall applied; dim = agent target / scope
	"client":             true, // an MCP client connected; dim = client app name
}

// IsAllowedMetric reports whether key may be recorded.
func IsAllowedMetric(key string) bool { return allowedMetrics[key] }

// dimPattern bounds a metric dimension to a short, non-identifying token —
// bucket labels (`1k-10k`), tool/command names (`search_symbols`), backend
// kinds (`sqlite`). Anything with a path separator, whitespace, or length
// beyond 32 is rejected, so even a caller that passes a path or a symbol name
// as a dimension cannot leak it: the counter falls back to the bare key.
var dimPattern = regexp.MustCompile(`^[A-Za-z0-9_.<>+-]{1,32}$`)

// safeDim returns dim when it is a bounded, non-identifying token, else "".
func safeDim(dim string) string {
	if dimPattern.MatchString(dim) {
		return dim
	}
	return ""
}

// BucketFileCount collapses a file count into a coarse bucket so an exact
// count — which can narrow identification — is never recorded.
func BucketFileCount(n int) string {
	switch {
	case n < 100:
		return "<100"
	case n < 1000:
		return "100-1k"
	case n < 10000:
		return "1k-10k"
	default:
		return "10k+"
	}
}

// BucketDuration collapses an elapsed time into a coarse bucket.
func BucketDuration(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return "<1ms"
	case d < 10*time.Millisecond:
		return "1-10ms"
	case d < 100*time.Millisecond:
		return "10-100ms"
	case d < time.Second:
		return "100ms-1s"
	case d < 10*time.Second:
		return "1-10s"
	default:
		return "10s+"
	}
}

// Rollup is one UTC day's aggregated, coarse counts. Counts maps an
// allow-listed metric key (optionally suffixed with a bucketed dimension after
// a colon) to the number of times it occurred that day.
type Rollup struct {
	Day    string         `json:"day"` // YYYY-MM-DD in UTC
	Counts map[string]int `json:"counts"`
}

// DayKey renders the UTC calendar day of t.
func DayKey(t time.Time) string { return t.UTC().Format("2006-01-02") }

// NewRollup creates an empty rollup for the UTC day of t.
func NewRollup(t time.Time) *Rollup {
	return &Rollup{Day: DayKey(t), Counts: map[string]int{}}
}

// metricName resolves an allow-listed (key, dim) pair to its counter name. ok
// is false when key is not on the allow-list. A dimension that is not a bounded
// token is dropped, so the bare key is returned — the path/name guard. This is
// the single place the allow-list and dimension sanitiser are applied, shared
// by Rollup.Add and the Recorder so they cannot diverge.
func metricName(key, dim string) (name string, ok bool) {
	if !allowedMetrics[key] {
		return "", false
	}
	if d := safeDim(dim); d != "" {
		return key + ":" + d, true
	}
	return key, true
}

// Add increments the counter for an allow-listed metric, optionally qualified
// by a dimension (a bucket label or a fixed enum like a tool name). It returns
// whether the event counted: a key not on the allow-list is silently dropped,
// and a dimension that is not a bounded token is discarded (the bare key still
// counts).
func (r *Rollup) Add(key, dim string) bool {
	name, ok := metricName(key, dim)
	if !ok {
		return false
	}
	if r.Counts == nil {
		r.Counts = map[string]int{}
	}
	r.Counts[name]++
	return true
}

// Merge folds other's counts into r. Days are assumed equal (the caller groups
// by day); a mismatched day is a programmer error and is ignored rather than
// silently corrupting r's day label.
func (r *Rollup) Merge(other *Rollup) {
	if other == nil || other.Day != r.Day {
		return
	}
	if r.Counts == nil {
		r.Counts = map[string]int{}
	}
	for k, v := range other.Counts {
		r.Counts[k] += v
	}
}

// Total is the sum of every counter — a convenience for "did anything happen
// today" checks before persisting or sending.
func (r *Rollup) Total() int {
	n := 0
	for _, v := range r.Counts {
		n += v
	}
	return n
}
