package indexer

import "github.com/zzet/gortex/internal/resolver"

// PURPOSE — expose a single boolean gate that controls whether the Temporal
// workflow/activity dispatch synthesiser runs for this indexer instance.
// RATIONALE — mirrors externalCallSynthesisEnabled so call-site code reads
// uniformly and the env-override logic stays in one place (the config helper).
// KEYWORDS — temporal, dispatch, synthesis, gate, config

// temporalDispatchEnabled reports whether the Temporal workflow/activity
// dispatch synthesiser should mint call edges for this indexer. The
// GORTEX_TEMPORAL environment variable takes precedence over the
// index.synthesize_temporal_dispatch config key (handled inside
// TemporalDispatchEnabledOrDefault). Default ON.
func (idx *Indexer) temporalDispatchEnabled() bool {
	return idx.config.TemporalDispatchEnabledOrDefault()
}

// temporalSkip returns the synthesizer skip map for RunFrameworkSynthesizersExcept.
// When temporal dispatch is disabled it returns a map gating out
// resolver.SynthTemporalStub; when enabled it returns nil (no skip).
// Centralising the logic here ensures all four call sites stay in sync.
//
// PURPOSE — single source of truth for the temporal-gate skip map so adding
// a new temporal sub-synthesizer only requires one edit here.
// RATIONALE — computing the map from temporalDispatchEnabled() at each call
// site avoids a new Indexer struct field while keeping call-site code uniform.
// KEYWORDS — temporal, skip, gate, synthesizer
func (idx *Indexer) temporalSkip() map[string]bool {
	if !idx.temporalDispatchEnabled() {
		return map[string]bool{resolver.SynthTemporalStub: true}
	}
	return nil
}
