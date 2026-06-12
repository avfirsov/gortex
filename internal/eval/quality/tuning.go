package quality

import (
	"fmt"
	"sort"
	"strings"
)

// SignalFeedback ties one rerank signal to its observed effect on
// agent-confirmed-useful results. Source: the existing `feedback`
// tool's per-symbol useful / not-needed / missing counters.
type SignalFeedback struct {
	Signal     string  `json:"signal"`
	Weight     float64 `json:"current_weight"`
	UsefulHits int     `json:"useful_hits"`
	MissedHits int     `json:"missed_hits"`
	// SuggestedWeight is the tuner's recommendation; the operator
	// decides whether to apply it. >Weight = the signal is producing
	// useful results and should be amplified; <Weight = it's
	// adding noise.
	SuggestedWeight float64 `json:"suggested_weight"`
	Reasoning       string  `json:"reasoning"`
}

// SuggestWeights consumes the per-signal feedback rows + a global
// nudge factor and emits a per-signal suggestion. Pure analysis —
// the rerank pipeline isn't mutated; the caller applies via
// `.gortex.yaml::search.weights`.
//
// Algorithm:
//   - signals with useful_hits > missed_hits get nudged UP by
//     min(0.1, (useful - missed) / 100 * nudge)
//   - signals with missed_hits > useful_hits get nudged DOWN by
//     the same formula
//   - signals with neither (no data) keep their current weight and
//     report "insufficient data"
//
// Nudge is the per-call cap; 1.0 means "max ±0.1 per call",
// 0.5 means "half of that". Keeping nudges small makes tuning a
// gradient process rather than a wholesale weight swap.
func SuggestWeights(rows []SignalFeedback, nudge float64) []SignalFeedback {
	if nudge <= 0 {
		nudge = 1.0
	}
	out := make([]SignalFeedback, 0, len(rows))
	for _, r := range rows {
		delta := 0.0
		reason := ""
		switch {
		case r.UsefulHits == 0 && r.MissedHits == 0:
			reason = "insufficient data (no useful / missed records this window)"
		case r.UsefulHits > r.MissedHits:
			diff := float64(r.UsefulHits - r.MissedHits)
			delta = clamp(diff/100.0*nudge, 0, 0.1)
			reason = fmt.Sprintf("useful_hits=%d > missed=%d → nudge up by %.3f",
				r.UsefulHits, r.MissedHits, delta)
		case r.MissedHits > r.UsefulHits:
			diff := float64(r.MissedHits - r.UsefulHits)
			delta = -clamp(diff/100.0*nudge, 0, 0.1)
			reason = fmt.Sprintf("missed_hits=%d > useful=%d → nudge down by %.3f",
				r.MissedHits, r.UsefulHits, -delta)
		default:
			reason = fmt.Sprintf("useful_hits == missed_hits (%d) → no change", r.UsefulHits)
		}
		r.SuggestedWeight = r.Weight + delta
		if r.SuggestedWeight < 0 {
			r.SuggestedWeight = 0
		}
		r.Reasoning = reason
		out = append(out, r)
	}
	// Stable display order: biggest absolute suggested change first
	// so the operator sees the action items at the top.
	sort.SliceStable(out, func(i, j int) bool {
		return absVal(out[i].SuggestedWeight-out[i].Weight) > absVal(out[j].SuggestedWeight-out[j].Weight)
	})
	return out
}

// RenderTuningMarkdown produces the operator-facing report. Columns:
// signal, current weight, useful / missed counts, suggested weight,
// reasoning. Tail summary: count of nudges with abs(delta) > 0.05
// so the operator knows whether anything substantial is on the
// table.
func RenderTuningMarkdown(rows []SignalFeedback) string {
	var b strings.Builder
	fmt.Fprintln(&b, "# Rerank weight-tuning suggestion")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "_Each row's `suggested_weight` is a calculated nudge from the current `.gortex.yaml::search.weights` value, based on the feedback log. **Operator decides whether to apply** — no automatic mutation._")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "| signal | current | useful | missed | suggested | Δ | reasoning |")
	fmt.Fprintln(&b, "|--------|--------:|-------:|-------:|----------:|--:|-----------|")
	substantial := 0
	for _, r := range rows {
		delta := r.SuggestedWeight - r.Weight
		if absVal(delta) >= 0.05 {
			substantial++
		}
		fmt.Fprintf(&b, "| %s | %.3f | %d | %d | %.3f | %+.3f | %s |\n",
			r.Signal, r.Weight, r.UsefulHits, r.MissedHits,
			r.SuggestedWeight, delta, r.Reasoning)
	}
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "**Substantial nudges (|Δ| ≥ 0.05): %d / %d signals.**\n",
		substantial, len(rows))
	return b.String()
}

// --- math helpers ---------------------------------------------------

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func absVal(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
