# Learnings for temporal-compare plan

## [2026-06-12] Initial bootstrap

### Repository structure
- Working dir: /mnt/d/CODE/gortex
- Branch: feat/temporal-fork-all
- Build: `go build -o gortex ./cmd/gortex/` (CGO required)
- Test: `go test -race ./...`

### Key integration points (from plan)
- Config: `internal/config/config.go` — add `SynthesizeTemporalDispatch *bool` near line 590 (SynthesizeExternalCalls)
- Config helper `ExternalCallSynthesisEnabledOrDefault` at line 1361 — mirror for temporal
- Env override pattern: `internal/indexer/external_call_synthesis.go:13`
- Synthesizer list: `internal/resolver/framework_synth.go:114` — `defaultFrameworkSynthesizers()`
- SynthTemporalStub const: `internal/resolver/framework_synth.go:57`
- Temporal entry at line 117: `{name: SynthTemporalStub, fn: ResolveTemporalCalls}`
- RunFrameworkSynthesizers at line 152
- 4 passes in temporal_calls.go:83
- Indexer runs synthesizers at line 2491: `resolver.RunFrameworkSynthesizers(idx.graph)`
- Analyzer cores: temporal_orphans.go:39 (standalone), synthesizers inline in tools_analyze_synthesizers.go:31, resolution_outcomes inline in tools_analyze_resolution_outcomes.go:45
- Cobra pattern: cmd/gortex/query.go
- Tests: internal/resolver/temporal_orphans_test.go, internal/mcp/tools_analyze_temporal_test.go, internal/indexer/temporal_e2e_test.go, internal/resolver/temporal_calls_test.go

## [T1 done] Config + env plumbing
- Field: IndexConfig.SynthesizeTemporalDispatch *bool (after SynthesizeExternalCalls, ~line 591 in config.go)
- Method: TemporalDispatchEnabledOrDefault() on IndexConfig — reads GORTEX_TEMPORAL env first, then tri-state field (nil→true, default ON)
- NOTE: env reading placed in config method (not indexer helper) so config-package test can cover it; indexer helper just calls config method
- Env helper: internal/indexer/temporal_dispatch.go → (idx *Indexer) temporalDispatchEnabled() calls idx.config.TemporalDispatchEnabledOrDefault()
- Env var: GORTEX_TEMPORAL (on/1/true=enabled, off/0/false=disabled, case-insensitive)
- Test: internal/config/temporal_dispatch_test.go — 17 cases (3 tri-state + 14 env-precedence), all PASS
- Watcher test failures in indexer are pre-existing inotify resource exhaustion, unrelated to this task

## [T2 done] Gate SynthTemporalStub
- RunFrameworkSynthesizersExcept(g, skip map[string]bool) added to framework_synth.go
- RunFrameworkSynthesizers is now a thin wrapper (zero behavior change)
- All 5 call sites in indexer.go updated to use temporalSkip() helper (brief said 4; 5th was at line 4186)
- temporalSkip() returns map{SynthTemporalStub:true} when !temporalDispatchEnabled()
- Gate-proof tests in internal/resolver/temporal_gate_test.go
- Env-path test covers GORTEX_TEMPORAL=off via config helper
- All pre-existing tests pass with flag unset (default ON)

## [T3 done] Extracted analyzer cores
- Package: internal/analyzer/
- AnalyzeSynthesizers(g graph.Store, opts ...SynthesizersOption) SynthesizersResult — synthesizers.go
- AnalyzeResolutionOutcomes(g graph.Store, reasonFilter string, limit int) ResolutionOutcomesResult — resolution_outcomes.go
- ClassifyUnresolved(g graph.Store, name, fromLang string) (reason string, candidates int) — resolution_outcomes.go
- OrphanReportToMap(rep resolver.TemporalOrphanReport) map[string]any — temporal_orphans.go
- MCP handlers refactored to call analyzer functions (thin wrappers)
- Outcome constants in mcp package are aliases to analyzer.Outcome* (no duplication)
- All pre-existing MCP tests pass; analyzer package tests pass; build clean

## [T4 done] gortex analyze CLI command
- File: cmd/gortex/analyze.go
- Test: cmd/gortex/analyze_test.go
- Flags: --kind (required), --path (default "."), --temporal (on|off, default on), --format (json|text, default text)
- Supported kinds: temporal_orphans, synthesizers, resolution_outcomes
- In-process indexing: graph.New() + indexer.New() + idx.IndexCtx() — NO daemon
- --temporal off: sets cfg.Index.SynthesizeTemporalDispatch = &false before indexer creation

## [T5 done] Comparison artifacts
- docs/temporal-compare/compare-temporal.md (opencode agent prompt)
- docs/temporal-compare/compare-temporal.ps1 (PowerShell driver)
- Both use exact JSON field names from analyzer package
- Deep Mode section documented in compare-temporal.md
