# [v1c4095b] Java Temporal Invoker Detection

## Problem

Gortex recognises Java Temporal only via `@ActivityInterface` / `@WorkflowInterface`
annotations (emitted as `EdgeAnnotated` → `annotation::java::ActivityInterface`).
Some codebases use a **custom invoker wrapper** instead of annotated interfaces.
The wrapper accepts the workflow/activity type as a **string argument**, analogous
to Go's `workflow.ExecuteActivity(ctx, name, ...)`.

These call sites are currently invisible to gortex (0 broken_dispatch).

## Design

Treat Java invoker calls the same way as Go `temporal.stub` edges:

1.  **Extractor** detects method calls on configured invoker types
2.  **Emits** `EdgeCalls` with `via=temporal.stub` + `temporal_name=<extracted name>`
3.  **Resolver** (`ResolveTemporalCalls`) already handles such edges for Go;
    extend it to also resolve Java→Go cross-language stubs
4.  **Three-layer trust** (heuristic → allowlist → LLM verify) applies automatically

## 1. Configuration

New field in `.gortex/temporal-allowlist.yaml` (already git-ignored):

```yaml
# Simple class names (no package) of Java Temporal invoker interfaces/classes.
# When gortex sees a method call on a receiver of one of these types,
# it will inspect the call for Temporal dispatch semantics.
java_temporal_invokers:
  - YourInvokerClass
```

Default: empty list → invoker detection OFF (zero behavioural change).

Loading: `java_temporal_invokers` is read alongside the existing `env_helpers`
allowlist in the same file, gated by `GORTEX_ALLOW_LOCAL_TEMPORAL`.

## 2. Extractor: Detecting Invoker Calls

File: `internal/parser/languages/java_temporal.go` (new)

### 2.1 When to inspect a method call

After the existing `emitMethod()` in `java.go`, walk each method body for
`method_invocation` nodes. A call is a **candidate** when:

- The method name is one of: `invokeAsync`, `invokeSync`, `signalWithStart`,
  `signal`, **or** any name listed under a new config key
  `java_temporal_invoker_methods` (default: the four above).
- The **receiver type's simple name** matches an entry in `java_temporal_invokers`.

Receiver type resolution (best-effort):

| Signal | Resolution |
|--------|-----------|
| `invoker.invokeAsync(...)` where `invoker` is a field of known type | Field declaration type |
| `this.invoker.invokeAsync(...)` | Same as above |
| `workflowInvoker.invokeAsync(...)` where `workflowInvoker` is a constructor param | Parameter type |
| `getInvoker().invokeAsync(...)` | Skip (too opaque) |

If the receiver type cannot be resolved, the call is skipped (false negatives
are acceptable; false positives are not).

### 2.2 Extracting the Temporal name

Given a candidate call, extract the workflow/activity name from the first
string-bearing argument:

| Priority | Argument pattern | Extraction | `temporal_env_source` |
|----------|-----------------|------------|----------------------|
| 1        | String literal: `"FooWorkflow"` | `FooWorkflow` | `exact` |
| 2        | `env.getProperty(KEY, DEFAULT)` or `env.getRequiredProperty(KEY)` | DEFAULT (or key name if no default) | `heuristic` |
| 3        | `@Value("${key:DEFAULT}")` field | DEFAULT | `heuristic` |
| 4        | Constant reference: `Constants.WF_NAME` | Const name as string | `const_ref` |
| 5        | Variable: `workflowType` | Variable name | `variable` |

For priority 2, also store the env key:

```
temporal_env_key = KEY    // e.g. "order.workflow.type"
```

### 2.3 Determining dispatch kind

| Method | `temporal_kind` | Name source arg index |
|--------|----------------|-----------------------|
| `invokeAsync(type, opts, input)` | `workflow` | 0 (first arg) |
| `invokeSync(type, opts, input, cls, timeout)` (5-arg) | `workflow` | 0 |
| `invokeSync(type, taskQueue, input, cls, timeout)` (4-arg) | `workflow` | 0 |
| `signalWithStart(signalName, signalPayload, type, input, opts)` | `workflow` | 2 (third arg) |
| `signal(workflowId, signalName, payload)` | — | Don't emit stub edge |

For `signalWithStart`, also emit `temporal_signal_name` from the first argument.

For `signal`, do **not** emit a `temporal.stub` edge — a signal is not a dispatch
(no new workflow is started; it targets a running one).

### 2.4 Edge emission

For each candidate call that yields a name:

```go
toID := "unresolved::temporal::" + kind + "::" + name

edge := &graph.Edge{
    From:   callerMethodID,
    To:     toID,
    Kind:   graph.EdgeCalls,
    FilePath: filePath,
    Line:   line,
    Origin: graph.OriginASTResolved,
    Meta: map[string]any{
        "via":                "temporal.stub",
        "temporal_name":      name,
        "temporal_kind":      kind,           // "workflow" or "activity"
        "temporal_env_source": source,         // "exact" | "heuristic" | "const_ref" | "variable"
    },
}
```

If `temporal_env_source` is `"heuristic"`, also set:

```go
edge.Meta["temporal_env_key"] = envKey   // the property key
```

For `signalWithStart`, additionally:

```go
edge.Meta["temporal_signal_name"] = signalName
```

### 2.5 WorkflowOptions extraction (optional, for task queue)

When the call includes a `WorkflowOptions` argument, gortex may extract
`.setTaskQueue(...)` from the builder chain. This is **optional** (Phase 2).
If extracted, store as:

```go
edge.Meta["temporal_task_queue"] = taskQueueName
```

## 3. Resolver Integration

File: `internal/resolver/temporal_calls.go`

### 3.1 Stub edge collection

`buildTemporalIndex()` already walks `EdgeCalls` edges looking for
`via=temporal.stub`. No changes needed — Java stub edges use the same
`via` value.

### 3.2 Cross-language resolution

`resolveTemporalCrossLanguage()` currently links Java `@WorkflowInterface`
methods to Go workflows. Extend it to also link Java `temporal.stub` edges:

- Java stub edge has `temporal_name="ProcessOrderWorkflow"`
- Go register edge has `temporal_name="ProcessOrderWorkflow"` (from `RegisterWorkflow`)
- Match by name → create resolved edge from Java caller → Go workflow function

The matching logic is identical to Go-to-Go stub resolution — the only
difference is the source language of the stub edge.

### 3.3 Convention matching for Java

When a Java stub edge has `temporal_name` matching a Go function with
suffix `Workflow` or `Activity` (convention index), treat the same way
as Go convention matches — apply the same confidence tier.

## 4. Three-Layer Trust (automatic)

The existing three-layer system applies without changes:

| Layer | Java stub with `exact` | Java stub with `heuristic` |
|-------|----------------------|---------------------------|
| 1. Heuristic | — | 0.4 (speculative, hidden) |
| 2. Allowlist | If invoker class in `java_temporal_invokers` → 0.6 (visible) | Same |
| 3. LLM verify | — | `temporal_verify` checks edge → confirmed / rejected / uncertain |

Layer 2 needs a small extension: currently `temporal_env_source=allowlist`
is set when the Go env-helper name matches the allowlist. For Java, set it
when the receiver class name matches `java_temporal_invokers`.

## 5. Test Fixtures

File: `internal/indexer/temporal_e2e_test.go`

```go
func TestTemporalE2E_JavaInvokerToGoBridge(t *testing.T) {
    dir := t.TempDir()

    // Java: class with invoker calls
    writeFile(t, filepath.Join(dir, "OrderManager.java"), `package com.example;
import io.temporal.workflow.WorkflowOptions;

public class OrderManager {
    private final Invoker invoker;

    public String startOrder(Object input) {
        WorkflowOptions options = WorkflowOptions.newBuilder()
            .setTaskQueue("order-wf").build();
        return invoker.invokeAsync("ProcessOrderWorkflow", options, input).block();
    }

    public String startWithDefault(Object input) {
        WorkflowOptions options = WorkflowOptions.newBuilder()
            .setTaskQueue("order-wf").build();
        return invoker.invokeAsync(
            env.getProperty("order.workflow.type", "ProcessOrderWorkflow"),
            options, input).block();
    }
}`)

    // Go: workflow implementation
    writeFile(t, filepath.Join(dir, "workflow.go"), `package main
import "go.temporal.io/sdk/worker"

func ProcessOrderWorkflow(ctx workflow.Context, input OrderInput) error { return nil }

func init() { worker.RegisterWorkflow(ProcessOrderWorkflow) }`)

    idx := newTestIndexerGoJava(g)
    // Configure invoker class
    idx.javaInvokers = map[string]bool{"Invoker": true}

    ctx := context.Background()
    stats, err := idx.Index(ctx, dir, dir)
    require.NoError(t, err)

    // Expectations:
    // 1. Java invokeAsync("ProcessOrderWorkflow", ...) → EdgeCalls with via=temporal.stub
    //    and temporal_name="ProcessOrderWorkflow", temporal_env_source="exact"
    // 2. Java invokeAsync(env.getProperty("key", "ProcessOrderWorkflow"), ...) →
    //    temporal_name="ProcessOrderWorkflow", temporal_env_source="heuristic"
    // 3. Go RegisterWorkflow(ProcessOrderWorkflow) → temporal_role=workflow
    // 4. Stub resolver lands Java→Go edge on ProcessOrderWorkflow
}
```

## 6. Implementation Order

| Step | What | Files | Dependency |
|------|------|-------|------------|
| 1 | Config: `java_temporal_invokers` list | `internal/parser/languages/java_temporal.go` (new) | — |
| 2 | Tree-sitter: detect invoker method calls | `internal/parser/languages/java_temporal.go` | Step 1 |
| 3 | Emit `EdgeCalls` with `via=temporal.stub` | `internal/parser/languages/java_temporal.go` | Step 2 |
| 4 | Resolver: process Java `temporal.stub` edges | `internal/resolver/temporal_calls.go` | Step 3 |
| 5 | Env-property detection (`getProperty`) | `internal/parser/languages/java_temporal.go` | Step 2 |
| 6 | Cross-language linking (Java invoker → Go workflow) | `internal/resolver/temporal_calls.go` | Step 4 |
| 7 | Allowlist tier: `java_temporal_invokers` → 0.6 | `internal/resolver/temporal_calls.go` | Steps 1, 4 |
| 8 | E2E tests | `internal/indexer/temporal_e2e_test.go` | Steps 3–6 |
| 9 | Config loading from `.gortex/temporal-allowlist.yaml` | config package + `java.go` | Step 1 |

## 7. Acceptance Criteria

1. `invokeAsync("ProcessOrderWorkflow", options, input)` → `broken_dispatch`
   with `temporal_name="ProcessOrderWorkflow"`, `temporal_env_source=exact`
2. `invokeAsync(env.getProperty("key", "Default"), options, input)` →
   `broken_dispatch` with `temporal_name="Default"`, `temporal_env_source=heuristic`
3. If Go repo has `RegisterWorkflow(ProcessOrderWorkflow)` → edge resolves
4. `signalWithStart("sigName", payload, "WfType", null, options)` →
   `temporal_name="WfType"` + `temporal_signal_name="sigName"`
5. Empty `java_temporal_invokers` → invoker detection OFF → 0 broken_dispatch
6. LLM verify: heuristic edges (0.4) checked → confirmed / rejected / uncertain
7. All existing tests still pass: `go test ./internal/...`

## 8. What NOT to do

- ❌ Do NOT hardcode any invoker class name in source code
- ❌ Do NOT add corporate names to test fixtures
- ❌ Do NOT modify the Go extractor logic
- ❌ Do NOT change `@ActivityInterface` / `@WorkflowInterface` detection
- ❌ Do NOT emit `temporal.stub` for `signal()` calls (signal ≠ dispatch)
