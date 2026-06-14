# [v1c4095b] Go Env-Helper Constant Resolution

## Problem

Go Temporal dispatch calls commonly centralise activity/workflow names in
package-level string constants:

```go
// config/env.go
const ACTIVITY_NAME_DEFAULT = "ChargeActivity"

// workflow.go
name := wfutils.GetEnvOrDefault(config.ACTIVITY_NAME_ENV, config.ACTIVITY_NAME_DEFAULT)
workflow.ExecuteActivity(ctx, name, ...)
```

Gortex's env-helper extraction (`goEnvHelperDefaultLiteral`) recognises the
helper call by name (allowlist match on `GetEnvOrDefault`) but only extracts
a **string-literal** 2nd argument. When the 2nd argument is a
`selector_expression` (`config.CONSTANT_NAME`) instead of a literal
(`"ChargeActivity"`), extraction returns `""` and the dispatch
lands as `broken_dispatch` with `name="activity"` (the trailing identifier of
the dispatch argument variable).

The resolver's `buildTemporalIndex` already builds a `constVal` index mapping
constant names → string values (from `KindConstant` nodes carrying
`Meta["value"]`). This index is used to resolve `temporal_name=<CONSTANT_NAME>`
in the stub-resolution loop. However, the env-helper extraction runs **before**
the resolver, in the parser — at which point the `constVal` index does not yet
exist.

**Impact:** In a large Go+Temporal monorepo (4 repos, 200+ activities), 91
`broken_dispatch` edges with `name="activity"` were observed, all caused by
`selector_expression` default arguments to env-helper calls.

## Observed Patterns

The following patterns were observed in production Go+Temporal codebases. Names
have been anonymised; the structural shapes are faithful.

### Pattern 1: Env-helper with constant default (the gap — 91 broken_dispatch)

```go
// activity/charge/config/env.go
package config

const (
    ACTIVITY_NAME_ENV     = "CHARGE_ACTIVITY_NAME"
    ACTIVITY_NAME_DEFAULT  = "ChargeActivity"       // ← the value gortex needs
    ACTIVITY_TIMEOUT_ENV  = "CHARGE_ACTIVITY_TIMEOUT"
    ACTIVITY_TIMEOUT_DEFAULT = "10"
)

// workflow/call_activity/charge.go
import (
    wfutils "example.com/app/wfutils"
    "example.com/app/activity/charge/config"
)

func CallCharge(ctx workflow.Context, input ChargeInput) error {
    name := wfutils.GetEnvOrDefault(config.ACTIVITY_NAME_ENV, config.ACTIVITY_NAME_DEFAULT)
    //                                   ^^^^^^^ selector   ^^^^^^^ selector_expression
    //                                   gortex already      gortex returns "" ← GAP
    //                                   extracts this
    envTimeout := wfutils.GetEnvOrDefault(config.ACTIVITY_TIMEOUT_ENV, config.ACTIVITY_TIMEOUT_DEFAULT)
    ao := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
        ScheduleToCloseTimeout: time.Second * timeout,
    })
    workflow.ExecuteActivity(ao, name, input)
    return nil
}
```

Every activity in the codebase follows this exact pattern: `config/` subdirectory
with `NAME_ENV` + `NAME_DEFAULT` constants, and a call-activity wrapper that
reads both through `wfutils.GetEnvOrDefault`. The observed codebase has 46+
activity directories, each with this structure.

### Pattern 2: Local constants in workflow file

```go
// workflow/call_activity/validate.go
const (
    VALIDATE_ACTIVITY_NAME_DEFAULT  = "ValidateActivity"
    VALIDATE_QUEUE_NAME_DEFAULT      = "validate-activities"
    VALIDATE_TIMEOUT_DEFAULT         = "10"
)

func CallValidate(ctx workflow.Context, input ValidateInput) error {
    activity := wfutils.GetEnvOrDefault(VALIDATE_ACTIVITY_NAME_ENV, VALIDATE_ACTIVITY_NAME_DEFAULT)
    // Same selector_expression gap, but constants are local (no config. prefix)
}
```

### Pattern 3: Workflow-level constants (not activity)

```go
// workflow/call_activity/send_notification.go
const SEND_NOTIFICATION_ACTIVITY_NAME_DEFAULT = "SendNotificationActivity"

func CallSendNotification(ctx workflow.Context, input NotificationInput) error {
    name := wfutils.GetEnvOrDefault(SEND_NOTIFICATION_NAME_ENV, SEND_NOTIFICATION_ACTIVITY_NAME_DEFAULT)
    ...
}
```

### Pattern 4: Queue name constants (not needed for dispatch resolution)

Several activities also define task queue constants (`QUEUE_NAME_DEFAULT`).
These are NOT needed for dispatch resolution (task queue ≠ activity name), but
the codebase consistently groups them with activity name constants:

```go
const (
    ACTIVITIES_QUEUE_NAME_DEFAULT  = "charge-activities"   // NOT dispatch-relevant
    ACTIVITY_NAME_DEFAULT          = "ChargeActivity"       // dispatch-relevant
)
```

### Observed constant naming conventions

| Prefix pattern | Example | Domain |
|---|---|---|
| `CHARGE_ACTIVITY_*` | `CHARGE_ACTIVITY_NAME_DEFAULT = "ChargeActivity"` | Billing |
| `CATALOG_ACTIVITY_*` | `CATALOG_ACTIVITY_NAME_DEFAULT = "CatalogActivity"` | Product catalog |
| `AGREEMENT_ACTIVITY_*` | `AGREEMENT_ACTIVITY_NAME_DEFAULT = "AgreementActivity"` | Agreement |
| `PARTY_ACTIVITY_*` | `PARTY_ACTIVITY_NAME_DEFAULT = "PartyActivity"` | Party/role |
| `NOTIFICATION_ACTIVITY_*` | `NOTIFICATION_ACTIVITY_NAME_DEFAULT = "NotificationActivity"` | Notifications |
| `INVENTORY_ACTIVITY_*` | `INVENTORY_ACTIVITY_NAME_DEFAULT = "InventoryActivity"` | Inventory |

The suffix is always `_NAME_DEFAULT`, the value is always the activity function
name (matching the `worker.RegisterActivity` call).

### Env-helper package

The observed codebase uses a shared `wfutils` package:

```go
import wfutils "example.com/app/wfutils"

// Function signature:
func GetEnvOrDefault(envKey string, defaultValue string) string
```

`GetEnvOrDefault` is already in gortex's built-in allowlist
(`goEnvHelperNames`). The extraction fires correctly — it's the
`selector_expression` argument that breaks.

## Design

Extend the env-helper default extraction to also resolve `selector_expression`
2nd arguments through the const index. Two approaches:

### Approach A: Parser-side resolution (recommended)

Extend `goEnvHelperDefaultLiteral` to:

1. When the 2nd argument is a `selector_expression` (e.g.
   `config.ACTIVITY_NAME_DEFAULT`), extract the
   trailing identifier (`ACTIVITY_NAME_DEFAULT`).
2. Store it on the stub edge as `temporal_default_const=<CONSTANT_NAME>` (new
   meta key).
3. The resolver already handles const-name resolution (lines 163–174 of
   `temporal_calls.go`): when `temporal_name` matches a key in `constVal`, it
   retries with the literal value. Extend this same logic to also check
   `temporal_default_const`.

This is minimal-impact: the parser emits a new meta key, the resolver adds one
more `constVal` lookup. No cross-phase coupling.

### Approach B: Resolver-side resolution (alternative)

Defer the resolution entirely to the resolver: the parser records
`temporal_default_selector="config.ACTIVITY_NAME_DEFAULT"`
on the stub edge, and the resolver resolves the selector through `constVal`
when processing env-default stubs. More flexible but requires the resolver to
understand selector syntax.

**Chosen: Approach A** — simpler, reuses existing `constVal` lookup logic, and
the trailing identifier is exactly what `constVal` keys on (constant nodes are
indexed by their `Name` field, which is the bare identifier).

## 1. Extractor Changes

File: `internal/parser/languages/golang_temporal.go`

### 1.1 Extend `goEnvHelperDefaultLiteral`

When the 2nd argument is a `selector_expression`, extract the trailing
identifier and return it with a new source marker:

```go
// After the existing string-literal check:
arg := args.NamedChild(1)
if arg == nil {
    return "", false
}

// Existing: string literal
if lit, ok := goStringLiteralValue(arg, src); ok {
    return lit, true
}

// NEW: selector_expression → trailing identifier
if arg.Type() == "selector_expression" {
    if field := arg.ChildByFieldName("field"); field != nil {
        return field.Content(src), true  // returns "ACTIVITY_NAME_DEFAULT"
    }
}
return "", false
```

### 1.2 New source marker

The existing `temporal_env_source` values are: `"os_getenv"`, `"allowlist"`,
`"heuristic"`. Add a new value for selector-expression defaults:

- `"const_ref"` — the default was extracted from a `selector_expression`
  argument, and the returned value is a **constant name** (not the literal
  activity name).

The caller (`goTemporalENVDefaultName`) sets
`temporal_env_source=const_ref` when the env-helper returns a selector-derived
value.

### 1.3 Stub edge meta

When `temporal_env_source=const_ref`, the stub edge carries:

```go
edge.Meta["temporal_name"] = "activity"              // unchanged — the runtime variable name
edge.Meta["temporal_name_origin"] = "env_default"    // unchanged
edge.Meta["temporal_env_source"] = "const_ref"        // NEW
edge.Meta["temporal_default_const"] = "ACTIVITY_NAME_DEFAULT"  // NEW
```

`temporal_name` stays as the variable name (`"activity"`) because that is
what the dispatch argument resolves to at the AST level. The constant name is
stored separately in `temporal_default_const`.

## 2. Resolver Changes

File: `internal/resolver/temporal_calls.go`

### 2.1 Extend stub resolution

After the existing const-name resolution (lines 163–174), add a lookup for
`temporal_default_const`:

```go
// Env-default const resolution: when the default was a selector_expression
// referencing a constant, look up the constant's literal value.
if handlerID == "" {
    if constName, _ := e.Meta["temporal_default_const"].(string); constName != "" {
        if v, ok := idx.constVal[constName]; ok && v != "" {
            if id, o, c := idx.lookup(s.kind, v, callerRepo); id != "" {
                handlerID, origin, conf = id, o, c
                e.Meta["temporal_const_value"] = v
            }
        }
    }
}
```

This mirrors the existing `temporal_name` → `constVal` resolution but operates
on `temporal_default_const` instead.

### 2.2 Confidence tier

`const_ref` defaults land at the same tier as `allowlist`-sourced defaults:

- The constant is **provably** a string value (the `constVal` index only
  contains constants with unambiguous string values).
- The env-helper was allowlist-matched (the extraction only fires for
  allowlist-recognised helper names).
- Therefore: `temporal_env_source=const_ref` → `origin=OriginASTInferred`,
  `confidence=0.6` (visible by default), same as `allowlist`.

The existing env-default confidence logic already handles this correctly —
any `temporal_env_source` that is not `"heuristic"` lands at the inferred
tier. `"const_ref"` qualifies automatically.

### 2.3 Convention fallback for const values

When `temporal_default_const` resolves to a literal value but no registered
handler matches, the convention fallback should also try the resolved literal:

```go
if handlerID == "" {
    if constName, _ := e.Meta["temporal_default_const"].(string); constName != "" {
        if v, ok := idx.constVal[constName]; ok && v != "" {
            if id := idx.lookupConvention(s.kind, v, callerRepo); id != "" {
                handlerID, origin, conf = id, graph.OriginASTInferred, 0.6
                e.Meta["temporal_const_value"] = v
                e.Meta["temporal_resolution_via"] = "convention"
            }
        }
    }
}
```

## 3. Additional Go Patterns (for completeness)

### 3.1 ALL_CAPS constants as dispatch names

When `workflow.ExecuteActivity(ctx, SOME_CONSTANT, ...)` is called with a
bare identifier that is ALL_CAPS (Go convention for package-level constants),
`goTemporalNameFromExpr` returns the constant name as the dispatch name. The
existing resolver logic (lines 163–174) already handles this via `constVal`
lookup. **No changes needed.**

### 3.2 Grule dynamic dispatch

Calls like `workflow.ExecuteActivity(ctx, grule.GetActivityName(data), ...)`
use a rules engine to determine the activity name at runtime. These cannot be
statically resolved and should remain as `broken_dispatch`. **No changes
needed.**

### 3.3 Literal-string dispatch

`workflow.ExecuteActivity(ctx, "ChargeActivity", ...)` already
works correctly. **No changes needed.**

## 4. Test Fixtures

File: `internal/indexer/temporal_e2e_test.go`

```go
func TestTemporalE2E_GoEnvConstDefault(t *testing.T) {
    dir := t.TempDir()

    // Go: config package with string constant
    writeFile(t, filepath.Join(dir, "config", "env.go"), `package config

const (
    ACTIVITY_NAME_ENV     = "CHARGE_ACTIVITY_NAME"
    ACTIVITY_NAME_DEFAULT  = "ChargeActivity"
    ACTIVITY_TIMEOUT_ENV  = "CHARGE_ACTIVITY_TIMEOUT"
    ACTIVITY_TIMEOUT_DEFAULT = "10"
)`)

    // Go: workflow that uses env-helper with constant default
    writeFile(t, filepath.Join(dir, "workflow.go"), `package main

import (
    "example.com/app/config"
    "example.com/app/wfutils"
    "go.temporal.io/sdk/workflow"
)

func MyWorkflow(ctx workflow.Context, input OrderInput) error {
    name := wfutils.GetEnvOrDefault(config.ACTIVITY_NAME_ENV, config.ACTIVITY_NAME_DEFAULT)
    workflow.ExecuteActivity(ctx, name, input)
    return nil
}`)

    // Go: activity implementation
    writeFile(t, filepath.Join(dir, "activity.go"), `package main

import "go.temporal.io/sdk/worker"

func ChargeActivity(ctx context.Context, input OrderInput) error { return nil }

func init() { worker.RegisterActivity(ChargeActivity) }`)

    idx := newTestIndexer(g)
    ctx := context.Background()
    stats, err := idx.Index(ctx, dir, dir)
    require.NoError(t, err)

    // Expectations:
    // 1. wfutils.GetEnvOrDefault(config.X, config.ACTIVITY_NAME_DEFAULT) →
    //    temporal_default_const="ACTIVITY_NAME_DEFAULT", temporal_env_source="const_ref"
    // 2. Resolver: ACTIVITY_NAME_DEFAULT → constVal["ChargeActivity"] →
    //    matches RegisterActivity(ChargeActivity)
    // 3. Stub resolves: MyWorkflow → ChargeActivity at confidence 0.6
}
```

## 5. Implementation Order

| Step | What | Files | Dependency |
|------|------|-------|------------|
| 1 | Extract selector_expression default in `goEnvHelperDefaultLiteral` | `internal/parser/languages/golang_temporal.go` | — |
| 2 | Emit `temporal_default_const` + `temporal_env_source=const_ref` | `internal/parser/languages/golang_temporal.go` | Step 1 |
| 3 | Resolver: look up `temporal_default_const` in `constVal` | `internal/resolver/temporal_calls.go` | Step 2 |
| 4 | Resolver: convention fallback for const-resolved values | `internal/resolver/temporal_calls.go` | Step 3 |
| 5 | E2E tests | `internal/indexer/temporal_e2e_test.go` | Steps 3–4 |

## 6. Acceptance Criteria

1. `wfutils.GetEnvOrDefault(config.KEY, config.CONST_DEFAULT)` where
   `config.CONST_DEFAULT = "ChargeActivity"` → stub resolves to
   `ChargeActivity` with `temporal_env_source=const_ref`
2. Confidence = 0.6 (inferred, visible by default) — same as `allowlist`
3. `temporal_const_value="ChargeActivity"` stamped on the edge
4. Convention fallback works when no `RegisterActivity` matches but a
   convention-named function exists
5. Existing string-literal defaults still work (no regression)
6. Existing `temporal_name` const resolution (bare ALL_CAPS dispatch args)
   still works (no regression)
7. All existing tests pass: `go test ./internal/...`

## 7. What NOT to do

- ❌ Do NOT hardcode any constant name in source code
- ❌ Do NOT modify `goTemporalNameFromExpr` — it correctly returns the
  trailing identifier for selector expressions
- ❌ Do NOT change the `constVal` index construction in `buildTemporalIndex`
- ❌ Do NOT resolve grule/dynamic dispatch — these must stay `broken_dispatch`
- ❌ Do NOT add corporate constant names to test fixtures
