## [2026-06-12] Execution plan

### Task order
G2 → G5 → G1 → G3 → G4 (per plan priority)

### G4 strategy
Start with verification test only; only fix if test genuinely fails.
resolveTemporalSignalQueryLinks is already cross-repo per plan notes.

### Confidence tiers
- Exact: 0.9
- Convention: 0.9 (if unique)
- Env-default (G1): temporalEnvDefaultConfidence = 0.4 + MetaSpeculative
- Fuzzy (G3): ≤0.5 + MetaSpeculative
