# Plan: `gortex analyze` (daemonless) + `--temporal on|off` toggle

## Goal
Let a user A/B-compare the fork's Temporal-resolution enhancements against "original"
behavior using ONE binary, no daemon, on Windows. Two capabilities:
1. A daemonless `gortex analyze --kind <k> --path <dir> [--temporal on|off] [--format json|text]`.
2. A `--temporal` toggle (+ env `GORTEX_TEMPORAL`, default `on`) that gates ONLY the
   four fork-added Temporal graph-mutating passes, all of which hang off the single
   `SynthTemporalStub` synthesizer.

## CLI contract (frozen — scripts depend on it)
```
gortex analyze --kind <kind> --path <dir> [--temporal on|off] [--format json|text]
```
- `--kind` required; MUST support `temporal_orphans`, `synthesizers`, `resolution_outcomes`.
- `--path` default `.`; indexed IN-PROCESS (no daemon, no socket).
- `--temporal` default `on`; `off` ⇒ skip `SynthTemporalStub` (= all 4 temporal passes).
- `--format` default `text`; `json` emits the analyzer's raw payload with the EXACT field
  names the MCP `analyze` tool returns.
- env `GORTEX_TEMPORAL=off` ≡ `--temporal off` (so a `gortex mcp` / daemon started with it
  also indexes in "original" mode).

## Constraints / must-not
- MUST be daemonless: never dial or spawn the daemon, never touch the AF_UNIX socket.
- Default ON everywhere: zero behavior change for existing users / tests when flag+env unset.
- Gate ONLY Temporal. Do NOT touch unrelated synthesizers. Leave Java `name=` PARSING alone
  (it lives in the parser, is additive metadata, creates no edges); gate only edge synthesis,
  which is entirely inside `SynthTemporalStub`.
- No analyzer-body duplication: extract shared cores callable from BOTH the MCP handler and
  the new CLI command.
- TDD: failing test first for each unit of behavior. `go build -o gortex ./cmd/gortex/`
  (needs CGO) and `go test -race ./...` must pass.

## Grounded integration points (from exploration)
- In-process index entry: `indexer.New(g, reg, cfg.Index, logger)` then `idx.IndexCtx(ctx, path)`
  — `cmd/gortex/index.go` (graph/registry/parser setup at ~lines 88–121); returns fully
  resolved graph in `g` (`graph.Store`, concrete `*graph.Graph`).
- Framework synthesizers run at `internal/indexer/indexer.go:2491` via
  `resolver.RunFrameworkSynthesizers(idx.graph)` (single-repo, deferResolve=false path).
- Synthesizer list: `defaultFrameworkSynthesizers()` `internal/resolver/framework_synth.go:114`;
  temporal entry `{name: SynthTemporalStub, fn: ResolveTemporalCalls}` at line 117; runner
  `RunFrameworkSynthesizers` at line 152. `SynthTemporalStub` const at line 57.
- The 4 temporal passes all live under `ResolveTemporalCalls` (`internal/resolver/temporal_calls.go:83`):
  `resolveTemporalWrapperCalls` (:100), `resolveTemporalExecutorFields` (:101),
  `resolveTemporalCrossLanguage` (:232). Java `name=` parse util `parseJavaAnnotationName` (:337)
  is parser-side, NOT gated.
- Config tri-state pattern to mirror: `IndexConfig.SynthesizeExternalCalls *bool`
  (`internal/config/config.go:590`) + `ExternalCallSynthesisEnabledOrDefault` (:1361) + env helper
  `internal/indexer/external_call_synthesis.go:13`.
- Analyzer cores:
  - `resolver.DetectTemporalOrphans(g graph.Store) TemporalOrphanReport`
    (`internal/resolver/temporal_orphans.go:39`) — already standalone, takes only graph.
    JSON keys: `broken_dispatch`, `signal_no_handler`, `query_no_handler`, `orphan_activity`,
    `orphan_workflow`, `totals` (see `internal/mcp/tools_analyze_temporal.go:16`).
  - `synthesizers` logic inline in `handleAnalyzeSynthesizers`
    (`internal/mcp/tools_analyze_synthesizers.go:31`); rows JSON: `synthesizer`, `provenance`,
    `edges`, `by_kind`, `samples`; top-level `synthesizers`, `total_edges`.
  - `resolution_outcomes` inline in `handleAnalyzeResolutionOutcomes`
    (`internal/mcp/tools_analyze_resolution_outcomes.go:45`); JSON: `by_reason`, `total`, `rows`
    (`from`,`to`,`edge_kind`,`name`,`reason`,`candidates`).
- Cobra pattern to mirror: `cmd/gortex/query.go` (`init()` registers flags + `rootCmd.AddCommand`),
  JSON pretty-print via `enc.SetIndent("", "  ")`.
- Existing tests to extend: `internal/resolver/temporal_orphans_test.go` (`newTemporalTestGraph`),
  `internal/mcp/tools_analyze_temporal_test.go`, `internal/indexer/temporal_e2e_test.go`,
  `internal/resolver/temporal_calls_test.go`.

---

## Tasks

- [x] **T1 — Config + env plumbing for the temporal gate**
  - Add `IndexConfig.SynthesizeTemporalDispatch *bool` (mapstructure/yaml `synthesize_temporal_dispatch`)
    in `internal/config/config.go` next to `SynthesizeExternalCalls`.
  - Add `(IndexConfig) TemporalDispatchEnabledOrDefault() bool` (nil ⇒ true).
  - Add env override resolving `GORTEX_TEMPORAL` (`on/1/true` vs `off/0/false`) layered over the
    config helper, following the `external_call_synthesis.go` pattern.
  - Tests: table test for the tri-state + env precedence (env overrides config; unset ⇒ on).

- [x] **T2 — Gate `SynthTemporalStub` in the resolution pipeline**
  - Make `RunFrameworkSynthesizers` skippable for named synthesizers (e.g.
    `RunFrameworkSynthesizersExcept(g, skip map[string]bool)`, keep `RunFrameworkSynthesizers`
    as a thin wrapper passing empty skip — no behavior change).
  - At `internal/indexer/indexer.go:2491`, pass `skip[SynthTemporalStub]=true` when the resolved
    temporal flag (T1) is OFF. Also gate the deferred/multi-repo call site so `GORTEX_TEMPORAL=off`
    works under the daemon too.
  - **Gate-proof tests (must actually demonstrate the gate works, not just compile):**
    - ON: build a fixture graph with a string-name activity dispatch; assert the synthesized
      workflow→activity edge (and/or activity caller) is PRESENT. Reuse `temporal_calls_test.go` /
      `newTemporalTestGraph` fixtures.
    - OFF: identical fixture, flag off ⇒ that edge is ABSENT, AND `DetectTemporalOrphans` reports
      MORE orphans than the ON run (positive proof the passes were actually skipped).
    - Cover ALL FOUR sub-passes, not just the first: include fixtures exercising the wrapper-by-name
      (P2), executor struct-field (P6), and Java→Go cross-language (#21) paths, and assert each
      contributes edges in ON and none in OFF. The gate is one switch but the test must prove it
      kills all four behaviors.
    - Env-path test: same assertion driven via `GORTEX_TEMPORAL=off` (not just the struct field),
      proving the env override reaches the pipeline.
  - **Regression guard:** run the full touched-package suites (`internal/resolver`,
    `internal/indexer`, `internal/mcp`) after the gate and confirm previously-passing tests still
    pass with the flag UNSET (default on) — i.e. existing behavior is byte-for-byte unchanged.

- [x] **T3 — Extract reusable analyzer cores (no body duplication)**
  - Extract `AnalyzeSynthesizers(g graph.Store) <struct>` and
    `AnalyzeResolutionOutcomes(g graph.Store) <struct>` from the two MCP handlers into callable
    functions returning the payload struct; refactor the MCP handlers to call them and marshal
    identically (preserve exact JSON field names + the `totals`/`by_reason` shapes).
  - `temporal_orphans` already has `DetectTemporalOrphans`; add a tiny `OrphanReportJSON` helper or
    reuse handler mapping so CLI emits the identical `{...,"totals":{...}}` shape.
  - Tests: assert refactor preserves output (golden/shape test) — existing
    `tools_analyze_temporal_test.go` must still pass; add minimal shape tests for the other two.

- [x] **T4 — `gortex analyze` daemonless cobra command**
  - New `cmd/gortex/analyze.go` mirroring `query.go`: flags `--kind` (required), `--path` (default `.`),
    `--temporal` (on|off, default on), `--format` (json|text, default text).
  - `RunE`: load config, apply `--temporal` onto `cfg.Index.SynthesizeTemporalDispatch`, build
    graph+registry+parser in-process, `idx.IndexCtx`, then dispatch `--kind` to the T3 cores
    against the resolved graph; print text or pretty JSON. NEVER touch the daemon.
  - Support at least the 3 mandatory kinds; route remaining kinds if cheap, else clear error
    listing supported kinds.
  - Register via `rootCmd.AddCommand(analyzeCmd)`.
  - Integration test: run the command in-process over a Temporal fixture dir; assert valid JSON for
    `temporal_orphans`, and `orphan_activity` count(off) ≥ count(on).

- [x] **T5 — Comparison artifacts (docs)**
  - `docs/temporal-compare/compare-temporal.md` — opencode prompt: runs the four
    `gortex.exe analyze ...` snapshots (orphans off/on, synthesizers on, resolution_outcomes off),
    builds an `off|on|delta` orphan table, reports `temporal-stub` edge count, breaks down
    resolution_outcomes, does a ground-truth caller spot-check, prints a verdict. Tells the agent
    to read the real JSON shape rather than assume field names; on command failure show stderr,
    don't fabricate numbers.
  - `docs/temporal-compare/compare-temporal.ps1` — optional Windows driver running the same four
    commands and printing the delta table. Use the exact JSON keys from T3.
  - **Optional deep mode (`-Deep` / a second prompt section):** instead of only the analyze
    one-shots, do a FULL from-scratch graph rebuild for EACH toggle state and compare more
    thoroughly — index `--path` twice (`--temporal off`, then `--temporal on`), and for each rebuilt
    graph capture: total node/edge counts, the `temporal_orphans` full report, the `synthesizers`
    `temporal-stub` edge list, and `resolution_outcomes` `by_reason`. Diff the two complete snapshots
    (not just totals): list the specific workflow→activity edges present only in ON, and the
    activities that flip from orphan→linked. Each rebuild MUST be a clean in-process index (no reused
    graph between modes) so the comparison reflects a true cold rebuild, mirroring how the daemon
    would index under each flag. Keep this mode opt-in (slower) and clearly separated from the fast
    default path.

---

## Final Verification Wave
- [x] `go build -o gortex ./cmd/gortex/` succeeds (CGO on).
- [x] `go test -race ./...` passes for all touched packages (config, resolver, indexer, mcp, cmd).
- [x] `gortex analyze --help` shows the frozen contract.
- [x] On a Temporal fixture: `gortex analyze --kind temporal_orphans --path <fix> --temporal off --format json`
      vs `--temporal on` produce DIFFERENT orphan counts (off ≥ on for `orphan_activity`), and
      `--kind synthesizers --temporal on` reports a non-zero `temporal-stub` `edges` count that the
      off run lacks.
- [x] Existing daemon/MCP `analyze` behavior unchanged when `GORTEX_TEMPORAL` unset (default on).
- [x] No daemon socket created by the `analyze` command (verify it runs with no daemon running).
- [x] Gate tests prove ALL FOUR temporal sub-passes (P2 / P6 / cross-language / temporal-stub) are
      active in ON and absent in OFF; env-driven `GORTEX_TEMPORAL=off` path covered.
- [x] Regression: touched-package suites green with the flag unset — no pre-existing test changed
      behavior because of the gate.
- [x] Deep-mode driver performs a true cold from-scratch rebuild per toggle state and diffs full
      snapshots (edges present only in ON; activities flipping orphan→linked), not just totals.
