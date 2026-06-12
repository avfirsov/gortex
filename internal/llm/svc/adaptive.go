package svc

import "github.com/zzet/gortex/internal/llm"

// maxAdaptiveDepth caps how deep an adaptive retry may bisect a single
// oversized assist request. Each level halves the candidate slice:
// depth 0 is the whole list (1 chunk), depth 1 is 2 chunks, depth 2 is
// 4, depth 3 is 8. Three halving levels is the "8x expansion" ceiling
// — beyond that a still-overflowing leaf is reported as a hard error.
const maxAdaptiveDepth = 3

// completeChunked runs call over the full candidate slice. When call
// fails with a model context-overflow error (llm.IsContextOverflow),
// it bisects the slice and retries each half — recursively, down to
// maxAdaptiveDepth — then merges the per-chunk results with merge.
//
// This is the chunk-bisection adaptive retry: a request too large for
// the model's context window is split into independently-sized pieces
// rather than failing outright. Non-overflow errors abort immediately
// (bisecting would not help). The bool return reports whether any
// bisection happened, so callers can surface that the result was
// assembled from chunks.
//
// call must be pure with respect to its input slice — it is invoked
// once per chunk — and merge must accept the per-chunk results in
// left-to-right order.
func completeChunked[C any, R any](
	cands []C,
	call func([]C) (R, error),
	merge func([]R) R,
) (result R, chunked bool, err error) {
	r, callErr := call(cands)
	if callErr == nil {
		return r, false, nil
	}
	if !llm.IsContextOverflow(callErr) || len(cands) <= 1 {
		var zero R
		return zero, false, callErr
	}
	return bisectChunked(cands, call, merge, 1)
}

// bisectChunked splits cands in two, runs call on each half, and
// recurses into a half that itself overflows until maxAdaptiveDepth.
// depth is the current bisection level (1 for the first split).
func bisectChunked[C any, R any](
	cands []C,
	call func([]C) (R, error),
	merge func([]R) R,
	depth int,
) (R, bool, error) {
	mid := len(cands) / 2
	parts := [][]C{cands[:mid], cands[mid:]}
	results := make([]R, 0, len(parts))
	for _, part := range parts {
		r, err := call(part)
		if err == nil {
			results = append(results, r)
			continue
		}
		if llm.IsContextOverflow(err) && len(part) > 1 && depth < maxAdaptiveDepth {
			sub, _, subErr := bisectChunked(part, call, merge, depth+1)
			if subErr != nil {
				var zero R
				return zero, false, subErr
			}
			results = append(results, sub)
			continue
		}
		var zero R
		return zero, false, err
	}
	return merge(results), true, nil
}
