# Plan: Temporal gap roadmap — resolver/extractor follow-ups

Source: `docs/temporal-compare/temporal-gap-roadmap.md` (5 gaps). Implement in priority
order. Every new resolution lives inside `ResolveTemporalCalls` / the framework
synthesizer, so the existing `--temporal off` gate keeps reproducing original behavior —
do not add anything that runs when the gate is OFF.

## Grounded reality (corrections to the roadmap's file refs)
- **Real Go Temporal extractor:** `internal/parser/languages/golang_temporal.go` (+ `golang.go`).
  The roadmap's `internal/extractor/golang/temporal.go` DOES NOT EXIST.
- Resolver core `internal/resolver/temporal_calls.go`:
  - `buildTemporalIndex` (~:802) builds `byKindName` (`kind::name`→handlers), `constVal`
    (const NAME→string literal, ambiguous names dropped), `funcByName` (Activity/Workflow
    convention funcs).
  - `resolveTemporalWrapperCalls` (~:495–607): single pass, discovery (stub edges with
    `temporal_name_param`) + emission (rewrites caller stubs using `arg_names`).
  - `resolveTemporalExecutorFields` (~:609–680): joins `temporal_recv_type`+`temporal_name_field`
    with constructor `executor_type`/`executor_field`/`executor_value`.
  - `lookup` (~:741–760): same-repo-unique→0.9 else workspace-unique→0.9 else "". The
    `sameRepo` preference is the Gap-3 pain point. Node carries `RepoPrefix` natively.
  - `lookupConvention` (~:768–794): only `HasSuffix(name,"Activity"/"Workflow")`.
  - `resolveTemporalSignalQueryLinks` (~:254–323): consumes `via=temporal.handler`
    (provider) + `temporal.signal-send`/`temporal.query-call` (consumer) → emits
    `temporal.signal-link`/`temporal.query-link`. **Already cross-repo.**
  - `temporalEnvDefaultConfidence = 0.4` (~:19); speculative edges set `graph.MetaSpeculative`.
- Extractor `golang_temporal.go`:
  - `goTemporalNameFromExpr` (~:432): string-literal→name; identifier→name; selector→field;
    **call_expression → "" (unhandled)** — Gap 2 entry.
  - `goTemporalEnvDefaultName` (~:476) + `goEnclosingFuncBody` (~:522): already trace
    `cmp.Or(os.Getenv(ENV),DEF)` and `os.Getenv`+reassign in the enclosing function →
    `temporal_name_origin=env_default`. Gap 1 = extend to helper-call patterns.
  - Emitted stub meta today: `temporal_kind`, `temporal_name`, `temporal_local`,
    `temporal_name_origin`, `temporal_name_param`, `temporal_name_field`,
    `temporal_recv_type`, `arg_names`.
- Test harness:
  - Unit: `internal/resolver/temporal_calls_test.go` — `newTemporalTestGraph()` +
    `addGoFunc(id,name,file,repo)` / `addStubCall(callerID,kind,name,file)` /
    `addGoRegister(...)`; assert `call.To == activity.ID`, `call.Origin`, `call.Confidence`.
    `RepoPrefix` set via the `repo` arg (cross-repo tests).
  - E2E: `internal/indexer/temporal_e2e_test.go` — `writeFile` + `newTestIndexer(g)` /
    `newTestIndexerGoJava(g)` + `idx.Index(dir)`; find stub by `e.Meta["via"]=="temporal.stub"`.
  - Extractor: `internal/parser/languages/go_temporal_test.go` — `runGoExtract(t,src)` +
    `temporalEdgesByVia(fix,"temporal.stub")`.

## Constraints
- TDD: failing test first per behavior. `go build -o gortex ./cmd/gortex/` (CGO) and
  `go test -race ./...` green.
- `--temporal off` must remain byte-identical to original (regression-check the gate tests).
- New heuristic resolutions (fuzzy / env-default / cross-repo) ride a sub-0.9 confidence and
  set `graph.MetaSpeculative` so they stay out of default high-confidence views.
- Acceptance criteria in the roadmap name the user's private work repos (not in this repo) —
  reproduce each with a SYNTHETIC fixture (unit + e2e) matching the same code shape.

---

## Tasks (priority order from the roadmap)

- [x] **G2 (P0) — func-returning-literal dispatch** (`func GetFooName() string { return "FooActivity" }` + `ExecuteActivity(ctx, pkg.GetFooName(), …)`)
  - Extractor (`golang_temporal.go`/`golang.go`): (a) when a function body is a single
    `return "<string literal>"`, stamp the function node meta `temporal_const_return="<literal>"`;
    (b) in `goTemporalNameFromExpr`, for a `call_expression` arg `X.Func()`/`Func()`, emit the
    stub with `temporal_name_func="<callee func name>"` instead of dropping it.
  - Resolver (`buildTemporalIndex`): new pass `indexFunctionReturnLiterals` that reads
    `temporal_const_return` off `KindFunction`/`KindMethod` nodes into `constVal`
    (funcName→literal); in the main resolve, a stub with `temporal_name_func` resolves
    funcName→literal→handler.
  - Tests: extractor (`runGoExtract`) asserts `temporal_const_return` + `temporal_name_func`;
    resolver `TestFuncConstResolution`; e2e with the two-file shape.
  - Accept: a `pkg.GetXxxName()` dispatch lands on the registered activity; edge carries
    `temporal_const_value`/origin marking it came from a const-returning func.

- [x] **G5 (P0) — iterative wrapper-following (depth > 1)**
  - Resolver: wrap `resolveTemporalWrapperCalls` in a loop (max 3 iterations), breaking when
    an iteration adds zero new `temporal.stub` edges. Track per-iteration new-stub count;
    ensure idempotence (don't double-emit on re-run).
  - Tests: `TestIterativeWrapperFollowing` (wrapper A → wrapper B → `ExecuteActivity`); assert
    the depth-2 dispatch resolves; assert convergence (no infinite growth) and that a single
    wrapper still resolves exactly as before (no regression).
  - Accept: depth-2 wrapper dispatch resolves to the target; depth-1 unchanged.

- [x] **G1 (P1) — env-with-fallback via helper calls**
  - Extractor (`goTemporalEnvDefaultName`): extend recognition so a dispatch arg that is a
    bare identifier assigned from a helper call whose name matches `GetEnvOrDefault` /
    `EnvOr` / `GetenvDefault` / `GetEnvDefault` (case-insensitive) with a string-literal 2nd
    arg yields `temporal_name=<literal>`, `temporal_name_origin=env_default`. Heuristic is on
    the helper NAME (cross-package body is invisible) — keep the helper-name set tight and
    documented. Confidence stays `temporalEnvDefaultConfidence` (speculative).
  - Tests: extractor + e2e `TestEnvFallbackResolution` for `x := wfutils.GetEnvOrDefault(ENV, "Charge"); ExecuteActivity(ctx, x, …)`; confirm the existing `cmp.Or`/`os.Getenv` paths still pass.
  - Accept: helper-based env-default dispatch resolves at speculative confidence with
    `temporal_name_origin=env_default`.

- [x] **G3 (P1) — cross-repo string dispatch + conservative fuzzy/convention**
  - Resolver `lookup`: stop preferring a same-repo candidate when the only correct handler is
    cross-repo. Concretely: for Temporal, if same-repo yields none AND workspace yields exactly
    one → take it (already); ADD: if same-repo yields one but there are equally-named cross-repo
    registered handlers, do not silently pick same-repo — fall to convention/fuzzy with a
    note. Prefer an **import-based signal**: if the workflow's file/package imports the
    candidate's package, boost that candidate.
  - `lookupConvention`: broaden from `HasSuffix(name,"Activity"/"Workflow")` to
    `Contains(funcName, core+"Activity")` where `core = TrimSuffix(name,"Activity")`.
  - Fuzzy fallback: only when exact + convention fail, match a func whose name contains the
    core name; emit at confidence ≤0.5 with `graph.MetaSpeculative`. Document the rule.
  - Tests: `TestCrossRepoStringDispatch` (activity in a different `RepoPrefix`, name not
    exactly registered) resolves; a NEGATIVE test that an unrelated same-core name does NOT
    get falsely linked at high confidence (precision guard).
  - Accept: cross-repo string dispatch resolves; fuzzy links are speculative-tier only.

- [x] **G4 (P2) — signal/query cross-repo verification (+ optional chaining)**
  - First: add `TestSignalCrossRepo` proving `SignalExternalWorkflow(…, "save-order-signal")`
    in one repo links to `SetSignalHandler/GetSignalChannel(…, "save-order-signal")` in another
    via `resolveTemporalSignalQueryLinks` (handler markers already exist per exploration).
  - If the test passes as-is → Gap 4 is already covered; record that. If it reveals a real
    miss (e.g. name normalization / case), fix minimally in `resolveTemporalSignalQueryLinks`.
  - Optional (only if cheap): transitive signal chaining W1→W2→W3.
  - Accept: cross-repo signal/query links resolve in a synthetic two-repo fixture.

---

## Final Verification Wave
- [x] `go build -o gortex ./cmd/gortex/` succeeds (CGO).
- [x] `go test -race ./...` green (esp. resolver, indexer, parser/languages, mcp, cmd).
- [x] Each gap has a failing-first test now passing (G2 func-const, G5 iterative wrapper,
      G1 helper env-default, G3 cross-repo + precision-negative, G4 cross-repo signal).
- [x] Gate regression: `--temporal off` / `GORTEX_TEMPORAL=off` still reproduces original —
      the existing `temporal_gate_test.go` suite passes unchanged, and the new resolutions do
      NOT fire when the gate is off.
- [x] Precision guard: G3 fuzzy and G1 env-default edges are `MetaSpeculative` / sub-0.9
      confidence; the negative G3 test confirms no high-confidence false link.
- [x] Roadmap file-path correction noted (extractor is `internal/parser/languages/golang_temporal.go`).
