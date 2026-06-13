## [2026-06-12] Initial orientation

### Key file paths (corrections to roadmap)
- Go Temporal extractor: `internal/parser/languages/golang_temporal.go` (+ `golang.go`)
- Resolver core: `internal/resolver/temporal_calls.go`
- Resolver tests: `internal/resolver/temporal_calls_test.go`
- E2E tests: `internal/indexer/temporal_e2e_test.go`
- Extractor tests: `internal/parser/languages/go_temporal_test.go`

### Resolver structure (temporal_calls.go)
- `buildTemporalIndex` (~:802): builds `byKindName`, `constVal`, `funcByName`
- `resolveTemporalWrapperCalls` (~:495–607): single pass, stub edges
- `resolveTemporalExecutorFields` (~:609–680): executor fields
- `lookup` (~:741–760): same-repo-unique→0.9 else workspace-unique→0.9 else ""
- `lookupConvention` (~:768–794): HasSuffix(name,"Activity"/"Workflow")
- `resolveTemporalSignalQueryLinks` (~:254–323): already cross-repo
- `temporalEnvDefaultConfidence = 0.4` (~:19)

### Extractor structure (golang_temporal.go)
- `goTemporalNameFromExpr` (~:432): call_expression → "" (unhandled — Gap 2)
- `goTemporalEnvDefaultName` (~:476): traces cmp.Or/os.Getenv — Gap 1 extends to helper calls

### Test harness
- Unit: `newTemporalTestGraph()` + `addGoFunc` / `addStubCall` / `addGoRegister`
- E2E: `writeFile` + `newTestIndexer(g)` + `idx.Index(dir)`
- Extractor: `runGoExtract(t,src)` + `temporalEdgesByVia(fix,"temporal.stub")`

### The --temporal gate
- Sacred: every new resolution lives inside ResolveTemporalCalls / framework synthesizer
- Gate test: `internal/resolver/temporal_gate_test.go`
- `--temporal off` must be byte-identical to original

### Confidence rules
- Exact matches: 0.9
- Heuristic/speculative: sub-0.9 (env-default: 0.4 via temporalEnvDefaultConfidence)
- Fuzzy fallback (G3): ≤0.5 with graph.MetaSpeculative

## [2026-06-12] G2 (P0) — func-returning-literal dispatch (DONE)

### Gap
`workflow.ExecuteActivity(ctx, pkg.GetFooName(), …)` where
`GetFooName() string { return "FooActivity" }` was unresolved: the dispatch
arg is a `call_expression`, which `goTemporalNameFromExpr` mapped to "".

### Extractor changes (golang_temporal.go + golang.go)
- New `goTemporalFuncCallName(node, src)` — reduces a `call_expression`
  dispatch arg to the callee func name (function field: identifier → its
  text; selector_expression → its `field` child). Did NOT extend
  `goTemporalNameFromExpr` itself (it must keep returning "" for
  call_expression so the env-default / register paths are unaffected);
  added a SEPARATE helper instead.
- New `goFuncConstReturnLiteral(decl, src)` — returns the single
  string-literal a func body unconditionally returns. GOTCHA: the Go
  grammar nests body statements inside a `statement_list` child of the
  `block`, NOT directly under it. `goFuncBody` returns the `block`; must
  descend one more level to `statement_list` before the single-statement
  scan. Verified the tree shape with a throwaway walker test.
- `goDeferredCall.tempNameFunc` new field. In the dispatch-detection block,
  when `goTemporalNameFromExpr(argNode)=="" && argNode.Type()=="call_expression"`,
  set tempKind/tempLocal + tempName=funcName (placeholder, keeps edge
  shape) + tempNameFunc=funcName.
- Stub-edge emission stamps `temporal_name_func=<func>` when tempNameFunc set.
- `emitFunction` + `emitMethod` stamp `temporal_const_return=<literal>` when
  `goFuncConstReturnLiteral` matches.

### Resolver changes (temporal_calls.go)
- `buildTemporalIndex`: new `indexFunctionReturnLiterals` pass folds
  funcName → `temporal_const_return` literal into the SAME `constVal` map,
  with the same ambiguity rule (a name mapping to two distinct literals is
  dropped). Uses a separate `constReturnAmbiguous` set so it doesn't
  interfere with the string-const ambiguity tracking that runs after it.
- Stub loop: new block right after the initial `idx.lookup`. Reads
  `temporal_name_func` off the edge → constVal[funcName]=literal →
  `idx.lookup(kind, literal)` (ast_resolved 0.9, deterministic — NOT
  speculative, no MetaSpeculative) → falls back to `lookupConvention`
  (inferred 0.6) when the literal names an unregistered activity. Records
  provenance via `temporal_const_value=<literal>`.

### Gate
- Sacred gate intact: temporal_const_return is inert node meta (no edges);
  all resolution lives in ResolveTemporalCalls. All temporal_gate_test.go
  cases pass. `go test -race ./internal/resolver/... ./internal/parser/languages/... ./internal/indexer/...` green.

### Tests added
- go_temporal_test.go: TestGoTemporal_FuncCallArg_EmitsNameFunc,
  _BareIdentifierCallee, _ConstReturnFunc_StampsTemporalConstReturn,
  _ConstReturnMethod_StampsTemporalConstReturn.
- temporal_calls_test.go: TestFuncConstResolution (+ _UnknownFuncStaysPlaceholder).
  Helpers: addStubCallNameFunc, addGoConstReturnFunc.
- temporal_e2e_test.go: TestTemporalE2E_GoFuncReturningLiteral.

## [2026-06-12] G5 (P0) — iterative wrapper-following depth > 1 (DONE)

### Gap
`resolveTemporalWrapperCalls` ran only ONCE. If wrapper A calls wrapper B
which calls `ExecuteActivity`, the depth-1 pass saw A→B but NOT B→ExecuteActivity
at A's call sites, because B's temporal.stub edge (marking B as a wrapper)
was not yet emitted when A's callers were scanned.

### Implementation (temporal_calls.go)
1. Added `countTemporalStubEdges(g graph.Store) int` — counts EdgeCalls with
   `via="temporal.stub"`. Used as the fixed-point sentinel.

2. In `ResolveTemporalCalls`, replaced single `resolveTemporalWrapperCalls(g)`
   call with a loop `for iter := 0; iter < 3; iter++` that breaks when
   `countTemporalStubEdges(g)` is unchanged after the pass.

3. In `resolveTemporalWrapperCalls`, added `fwdParam` to the `pending` struct.
   In `emit`, when the forwarded argument is itself a `#param:<name>` node
   with a `position` key, set `fwd = name`. The emitted stub then carries
   `temporal_name_param=fwdParam`, marking the caller as a transitive wrapper
   so the NEXT iteration picks it up.

4. Added idempotence guard `temporalWrapperStubExists(g, from, kind, name)`
   before `g.AddEdge`. Prevents re-minting stubs across iterations which would
   replace the stored *Edge pointer and break already-retargeted in-edges.

### Tests added (temporal_calls_test.go)
- Test helpers: `addWrapperStub`, `addWrapperCall`, `wfChargeStub`,
  `buildDepth2WrapperGraph`.
- `TestIterativeWrapperFollowing` — single pass fails; full
  `ResolveTemporalCalls` loop resolves depth-2 chain to registered activity.
- `TestIterativeWrapperFollowing_ConvergesWithNoGrowth` — after fixed point,
  further passes add zero new stubs.
- `TestIterativeWrapperFollowing_Depth1Regression` — depth-1 wrapper still
  resolves correctly.

### Verification
`go test -race -count=1 ./internal/resolver/...` green.
`go build -o /tmp/gortex-g5 ./cmd/gortex/` clean.

## [2026-06-12] G1 (P1) — env-with-fallback via helper calls (DONE)

### Gap
`actName := wfutils.GetEnvOrDefault(ENV_KEY, "ChargeActivity")` then
`ExecuteActivity(ctx, actName, …)` did NOT resolve. `goTemporalEnvDefaultName`
only recognised `cmp.Or(os.Getenv(KEY), "lit")` (via `goCallEnvDefaultLiteral`)
and the `os.Getenv` + `if name==""` reassign shape. A project-local
env-or-default helper is neither — its body lives in another package and is
invisible at extract time, so the os.Getenv-anchored shapes never fired.

### Implementation (golang_temporal.go — EXTRACTOR ONLY)
- New `goEnvHelperNames` allow-list (4 names): `GetEnvOrDefault`, `EnvOr`,
  `GetenvDefault`, `GetEnvDefault`. TIGHT by design — precision over recall;
  a wrong guess mints a speculative edge onto the wrong activity.
- New `goEnvHelperDefaultLiteral(call, src)` — matches the callee NAME only
  (bare identifier OR selector_expression's trailing `field`), case-insensitive
  via `strings.EqualFold`. On a match, returns the string-literal 2nd argument
  (`args.NamedChild(1)`, requires ≥2 args) as the default.
- Wired into `goTemporalEnvDefaultName`'s existing call_expression branch as a
  third `else if` after `goCallEnvDefaultLiteral`. No new node meta, no new
  field on the deferred-call struct: the existing call site in golang.go
  (`if def, ok := goTemporalEnvDefaultName(...)` → `dc.tempEnvDefault=true`)
  already stamps `temporal_name_origin=env_default`, so the stub-emission +
  resolver speculative tier (`temporalEnvDefaultConfidence=0.4`,
  `graph.MetaSpeculative`) are reused unchanged.
- Added `"strings"` import (was not previously imported in this file).

### Gate / scope
- NO resolver changes (`temporal_calls.go` untouched). G1 is extractor-only;
  the env_default speculative tier was already wired by the prior env work.
  All `temporal_gate_test.go` cases pass — the new path only refines the
  already-gated env-default stub name, no new edge surface.
- G2 / G5 untouched (separate code paths).

### Tests added
- go_temporal_test.go: `TestEnvFallbackViaHelper_GetEnvOrDefault` (selector
  helper), `TestEnvFallbackViaHelper_BareIdentifierEnvOr` (bare-ident helper),
  `TestEnvFallbackViaHelper_UnknownHelperNotFlagged` (negative — name not in
  allow-list stays as the variable identifier, no env_default flag).
- temporal_e2e_test.go: `TestEnvFallbackResolution` — full pipeline lands the
  helper-named dispatch on the default activity at the speculative tier
  (env_default origin + OriginSpeculative + MetaSpeculative).

### Gotcha
- `goStringLiteralValue` only accepts `interpreted_string_literal` /
  `raw_string_literal`, so a non-literal 2nd arg (e.g. a const ident) yields
  `("", false)` — exactly the desired conservative behaviour.

### Verification
`go test -race -count=1 ./internal/parser/languages/... ./internal/resolver/... ./internal/indexer/...` green.
`go build -o /tmp/gortex-g1 ./cmd/gortex/` clean.

## G3 (P1) — cross-repo string dispatch + conservative fuzzy/convention

### The real gap (vs the task's framing)
- `lookup` (exact, register-confirmed) already handles cross-repo: same-repo
  unique → 0.9, workspace-unique → 0.9. UNTOUCHED, as required.
- The gap was `lookupConvention`: it did an EXACT-KEY lookup
  `idx.funcByName[name]`. funcByName is keyed by the bare FUNC name
  ("ChargeActivity"), so a dispatch of bare "Charge" produced
  `funcByName["Charge"]` == empty → no convention match, ever. The cross-repo
  ChargeActivity was invisible to dispatch "Charge".

### lookupConvention — broadened to iterate-all + suffix-AND-core-contains
- Now iterates ALL funcByName entries (not exact key). Predicate:
  `HasSuffix(fn, suffix) && Contains(fn, core)` where
  `core = strings.TrimSuffix(name, suffix)`.
- Backward-compatible: for name="ChargeActivity", core="Charge",
  HasSuffix("ChargeActivity","Activity") && Contains(...,"Charge") still true.
- For name="Charge", core="Charge" → matches "ChargeActivity",
  "MyChargeActivity", etc. Same-repo-unique → ID; all-unique → ID; else "".
- Confidence unchanged: caller still stamps convention at OriginASTInferred / 0.6.

### lookupFuzzy — new conservative last-resort
- Fires ONLY when exact + const + convention all returned "".
- Tries the STRICTER raw dispatch `name` containment first; only widens to the
  trimmed `core` when raw-name matches nothing (tighter needle before looser).
- Requires EXACTLY ONE kind-suffixed candidate across the workspace — any
  ambiguity abstains. Returns the single candidate ID or "".
- Caller stamps: `graph.OriginSpeculative`, conf 0.5,
  `temporal_resolution_via="fuzzy"`, `e.Meta[graph.MetaSpeculative]=true`.

### GOTCHA — case-sensitive Contains bit me in the test
- `strings.Contains` is case-sensitive (by design, conservative). "Overcharge"
  does NOT contain core "Charge" (capital C) — only lowercase "charge". My
  first ambiguator "OverchargeActivity" therefore did NOT ambiguate convention,
  so convention resolved (0.6) and fuzzy never fired. Switched the ambiguating
  sibling to "ChargebackActivity" (contains capital "Charge", but NOT the raw
  "ChargeActivity"), which makes convention ambiguous (2 core matches) while
  fuzzy's raw-name single-match carries "ChargeActivity".

### Wiring
- Fuzzy block sits AFTER the convention fallback, BEFORE env-default handling,
  in the main stub-resolution loop. Mirrors the existing convention block shape.
- Re-orphan path (`handlerID==""`) already deletes `graph.MetaSpeculative`;
  `temporal_resolution_via` is left like the convention path does (harmless —
  the placeholder To + empty Origin already signal unresolved).

### Tests (TDD, red-first confirmed)
- temporal_calls_test.go:
  - `TestCrossRepoStringDispatch` — "Charge" in repoA → "ChargeActivity" in
    repoB via broadened convention (conf ≤ 0.6).
  - `TestCrossRepoStringDispatch_NegativePrecision` — "ProcessActivity" alone
    does NOT satisfy "Charge" (resolved=0); after adding "ChargeActivity" it
    wins and ProcessActivity stays unlinked (0 inbound edges).
  - `TestFuzzyFallback_SpeculativeTier` — ambiguous convention forces fuzzy;
    asserts OriginSpeculative + conf ≤ 0.5 + MetaSpeculative + via=fuzzy.

### Verification
- `go build -o /tmp/gortex-g3 ./cmd/gortex/` clean.
- `go test -race -count=1 ./internal/resolver/...` green (incl.
  temporal_gate_test.go).
- `go test -race -count=1 ./internal/indexer/...` green.

### Scope
- Resolver-only. No extractor changes. G1/G2/G5 code paths untouched.

## G4 (P2) — signal/query cross-repo verification (ALREADY COVERED)

### Finding
`resolveTemporalSignalQueryLinks` was already cross-repo. The function iterates
ALL `g.EdgesByKind(graph.EdgeCalls)` without any repo filter when building the
`providers` map and when scanning consumers. A `via=temporal.signal-send` edge
in repoA links to a `via=temporal.handler` edge in repoB with matching
`temporal_kind::temporal_name` key, emitting `via=temporal.signal-link` — no
code changes needed.

### Test added (TDD — confirmed RED-would-be-redundant)
`TestSignalCrossRepo` in `internal/resolver/temporal_calls_test.go`:
- repoA: `SenderWF` with `via=temporal.signal-send`, `temporal_name="save-order-signal"`
- repoB: `ReceiverWF` with `via=temporal.handler`, `temporal_name="save-order-signal"`
- After `ResolveTemporalCalls(g)`: asserts EdgeCalls from SenderWF→ReceiverWF
  with `via=temporal.signal-link`, `temporal_name`, `temporal_kind` all correct.
- Test PASSED on first run (no implementation changes).

### Verification
`go test -race -count=1 ./internal/resolver/...` green (all tests including
temporal_gate_test.go).

## [2026-06-13] G2 (func-returning-literal dispatch) restored after git stash loss

### What was lost / restored
`goTemporalFuncCallName` and `goFuncConstReturnLiteral` were missing from
`internal/parser/languages/golang_temporal.go` — added after the closing `}` of
`goTemporalNameFromExpr`, before `goTemporalEnvDefaultName`.

`goFuncBody` already existed in `golang.go` (line 1143); `goStringLiteralValue`
already existed in `golang_temporal.go` (line 675) — no new helpers needed.

`internal/resolver/temporal_calls.go` — two insertions:
1. `temporal_name_func` resolution block added in the stub loop between the
   existing const-named dispatch block and the convention fallback block.
2. `indexFunctionReturnLiterals` closure + two iteration loops added in
   `buildTemporalIndex` after the `constAmbiguous` map declaration (around
   line 1011 in the original file), before Phase 1 Go-side registration.

`internal/resolver/temporal_calls_test.go` — two helper methods
(`addStubCallNameFunc`, `addGoConstReturnFunc`) inserted before `addGoRegister`;
two tests (`TestFuncConstResolution`, `TestFuncConstResolution_UnknownFuncStaysPlaceholder`)
appended at end of file.

### Verification
`go build -o /tmp/gortex-g2fix ./cmd/gortex/` — clean.
`go test -race -count=1 ./internal/resolver/... ./internal/parser/languages/...` — all green.

## [2026-06-13] G2 extractor tests + E2E test restored

### What was added
Four extractor tests added to `internal/parser/languages/go_temporal_test.go`
before `TestGoTemporal_SignalExternalWorkflow` (after line 479 in the original):

- `TestGoTemporal_FuncCallArg_EmitsNameFunc` — selector-qualified call arg
  (`pkg.GetChargeName()`) emits `temporal_name_func="GetChargeName"` on stub.
- `TestGoTemporal_FuncCallArg_BareIdentifierCallee` — bare call arg
  (`GetChargeName()`) also captured under `temporal_name_func`.
- `TestGoTemporal_ConstReturnFunc_StampsTemporalConstReturn` — single-return
  func stamped `temporal_const_return="ChargeActivity"`; multi-return func NOT.
- `TestGoTemporal_ConstReturnMethod_StampsTemporalConstReturn` — method with
  single `return "<lit>"` also stamped.

`extractedFixture` has no plain `nodes` field; iterate
`fix.nodesByKind[graph.KindFunction]` / `fix.nodesByKind[graph.KindMethod]`.

One E2E test added to `internal/indexer/temporal_e2e_test.go`:
- `TestFuncConstReturnDispatch_E2E` — same-package helper
  `names_GetChargeName() string { return "ChargeActivity" }` called as
  dispatch arg; resolves to registered `ChargeActivity` node after
  `idx.Index(dir)`.

### Verification
`go test -race -count=1 ./internal/parser/languages/... ./internal/indexer/...` — all green.
