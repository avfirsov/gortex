package mcp

import (
	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/search"
)

// SetSearchConfig installs the `.gortex.yaml::search` block on the
// server. Called by the server / daemon entrypoint right after
// NewServer, alongside SetArtifacts / SetNamedQueries. The block
// supplies the keyword-soup rewrite mode, equivalence-class
// expansion settings, and the prose-indexing toggle consumed by the
// search handlers. A no-op-friendly zero value keeps every knob at
// its documented default.
func (s *Server) SetSearchConfig(cfg config.SearchConfig) {
	s.searchCfg = cfg
	// Build the curated equivalence table once, merging in any
	// repo-custom classes. Cheap and immutable -- safe to do here
	// rather than lazily, and no lock is needed afterwards.
	s.equivalence = search.NewEquivalenceTable(cfg.EquivalenceExtra)
}

// searchConfig returns the installed search config. The zero value is
// valid -- every accessor on config.SearchConfig folds an empty field
// into its default.
func (s *Server) searchConfig() config.SearchConfig {
	return s.searchCfg
}
