package semantic

import "context"

// The apply gate is the warmup barrier between enrichment COMPUTE and
// enrichment APPLY. During a cold index the enrichment pool overlaps the
// resolve phase: compute (go/packages loads, tree-sitter parses) touches no
// graph state and is free to run, but a graph apply that lands mid-resolve
// holds the shared ResolveMutex in long stretches and starves the resolver —
// measured as a compute loop stretched 289s → 2,193s with minutes-long
// zero-progress plateaus. Providers therefore wait on this gate immediately
// before their apply phase; the daemon closes it the moment the warmup
// resolve completes. A context without a gate (every non-warmup path) never
// waits.

type applyGateKey struct{}

// WithApplyGate attaches the warmup apply barrier to a provider context.
func WithApplyGate(ctx context.Context, gate <-chan struct{}) context.Context {
	if gate == nil {
		return ctx
	}
	return context.WithValue(ctx, applyGateKey{}, gate)
}

// ApplyGateWait blocks until the context's apply gate opens, the context is
// cancelled, or immediately when no gate is attached.
func ApplyGateWait(ctx context.Context) error {
	gate, _ := ctx.Value(applyGateKey{}).(<-chan struct{})
	if gate == nil {
		return nil
	}
	select {
	case <-gate:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
