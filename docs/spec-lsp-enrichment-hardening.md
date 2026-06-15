# LSP Enrichment Hardening — Technical Specification

## Problem Statement

An LSP-based enrichment pipeline processes large codebases (200K+ nodes) by sending
`textDocument/hover` requests to a language server. The current implementation has
three critical issues that cause the language server process to crash during enrichment,
resulting in ~90% hover errors and ~1.5% successful enrichments.

The three issues form a cascade: Issue 1 amplifies Issue 2, which triggers Issue 3.
Once Issue 3 fires, all subsequent hover requests fail because the server is dead.

---

## Issue 1: No Concurrency Throttling in Hover Loop

### Description

The enrichment loop iterates over all graph nodes and sends a hover request for each
one sequentially. Despite a `maxParallel` configuration field (default: 10) existing on
the provider struct, it is **never referenced** in the hover loop.

This means a single enrichment pass sends 200K+ sequential requests with no pause,
overwhelming the language server with a continuous stream of requests.

### Proposed Fix

Introduce a semaphore pattern using `maxParallel`:

```
sem := make(chan struct{}, maxParallel)
var wg sync.WaitGroup

for each node {
    wg.Add(1)
    sem <- struct{}{} // acquire slot

    go func(node) {
        defer func() {
            <-sem        // release slot
            wg.Done()
        }()

        // hover logic for this single node
    }(node)
}

wg.Wait()
```

**Key points:**
- The semaphore limits concurrent goroutines to `maxParallel` (default 10).
- Each goroutine handles exactly one node: open doc → hover → close doc (see Issue 2).
- `WaitGroup` ensures all goroutines complete before the enrichment function returns.
- Error counters remain race-safe (use `atomic.Int64` or a mutex-protected counter).

### Expected Impact

From 200K sequential requests → max 10 concurrent requests at any time. The language
server receives a manageable, bounded request rate.

---

## Issue 2: Document Lifecycle Leak (didOpen without didClose)

### Description

The current code has two phases:

1. **Pre-open phase**: Before the hover loop starts, `textDocument/didOpen` is sent for
   every file that contains target nodes. This can be tens of thousands of files.
2. **Hover loop**: Each hover request may additionally open documents for nodes from
   the full node set.
3. **Close phase**: `textDocument/didClose` is sent in a single `defer` block **after
   the entire hover loop completes**.

This means at peak, the language server holds tens of thousands of open documents in
memory simultaneously. For a Java language server, each open document consumes heap
memory for AST, index entries, and semantic analysis state. This leads to Java heap
OOM and process termination.

### Proposed Fix

Eliminate the bulk pre-open and bulk close phases entirely. Instead, make document
lifecycle **per-goroutine**:

```
go func(node) {
    // 1. Open document for this node's file
    didOpen(file)

    // 2. Send hover request
    result := hover(file, position)

    // 3. Immediately close the document
    didClose(file)

    // 4. Process hover result
    extractAndEnrich(result)
}(node)
```

**Key points:**
- At any time, at most `maxParallel` documents are open simultaneously (bounded by
  the semaphore from Issue 1).
- Each document is closed immediately after its hover completes — no accumulation.
- Remove the pre-open loop entirely.
- Remove the bulk defer-close block entirely.
- Track open documents per-goroutine (a simple `map[string]bool` or just the file
  path string) to ensure `didClose` is always called even on hover errors.

### Error Handling

If hover fails for a node, the goroutine must still close the document:

```
didOpen(file)
result, err := hover(file, position)
didClose(file) // always, even on error
if err != nil { ... }
```

### Expected Impact

From tens of thousands of simultaneously open documents → at most `maxParallel` (10)
open at any time. Java heap usage drops by orders of magnitude.

---

## Issue 3: No Reconnection with Backoff on Server Exit

### Description

When the language server process exits (OOM, crash, etc.), subsequent hover requests
fail with an error like `"LSP server exited"`. The current code counts these errors
but **continues sending requests to the dead process** for the remainder of the loop.

There is existing infrastructure for:
- Resetting connection state (closing client, clearing document tracking maps)
- Exponential backoff for initial connection (`dialBackoff`, starting at 100ms)
- Re-establishing the client

However, none of this is used during the enrichment hover loop when the server dies
mid-flight.

### Proposed Fix

Add a `reconnectWithBackoff()` method that:

1. Closes all tracked open documents (best-effort `didClose` for each).
2. Resets connection state (nil the client reference, clear document maps).
3. Sleeps with exponential backoff (start: 100ms, max: 30s, multiplier: 2x).
4. Re-establishes the client connection.
5. Returns error if reconnection fails after max attempts (e.g., 5 retries).

Integration into the hover loop:

```
result, err := hover(file, position)
if err != nil && isServerExitError(err) {
    // attempt reconnection
    if reconnectErr := reconnectWithBackoff(); reconnectErr != nil {
        // fatal: stop enrichment, return error
        return reconnectErr
    }
    // retry this node's hover once after reconnection
    result, err = hover(file, position)
}
```

**New struct fields needed:**
- `reconnectMu sync.Mutex` — prevents concurrent reconnection attempts from multiple
  goroutines (only one goroutine should reconnect, others should wait).
- `maxDialBackoff time.Duration` — cap for exponential backoff (default: 30s).

**Server exit detection:**
Check if the hover error message contains known server-exit patterns. At minimum,
check for the string `"LSP server exited"`. Consider also checking for transport-level
errors (broken pipe, connection reset).

**Concurrency safety:**
When multiple goroutines detect server exit simultaneously:
- First goroutine to acquire `reconnectMu` performs reconnection.
- Other goroutines wait for reconnection to complete, then retry their hovers.
- Use a `sync.Once`-like pattern or a "reconnecting" flag under the mutex.

### Expected Impact

When the language server crashes mid-enrichment, the system recovers automatically
instead of failing 195K remaining hovers. After reconnection, enrichment continues
with a fresh server instance. The backoff prevents tight retry loops on persistent
failures.

---

## Implementation Order

The three fixes should be implemented in order, as each builds on the previous:

1. **Issue 2 first** (doc lifecycle): This is the root cause of the OOM. Fixing it
   independently reduces heap pressure even without throttling.
2. **Issue 1 second** (throttling): With per-goroutine doc lifecycle, the semaphore
   bounds both concurrency and open documents simultaneously.
3. **Issue 3 last** (reconnection): With throttling and proper doc lifecycle, the
   server is far less likely to crash. But reconnection is still needed as a safety
   net for edge cases.

---

## Acceptance Criteria

1. **No bulk didOpen**: No code path opens more than `maxParallel` documents
   simultaneously during enrichment.
2. **Per-item doc lifecycle**: Every `didOpen` has a corresponding `didClose` in the
   same goroutine, regardless of hover success/failure.
3. **Concurrency bounded**: At most `maxParallel` goroutines run concurrently during
   the hover loop.
4. **Recovery on server exit**: When hover returns a server-exit error, the system
   attempts reconnection with exponential backoff (min 3 retries, max backoff 30s).
5. **Concurrent reconnect safety**: Multiple goroutines detecting server exit do not
   trigger multiple simultaneous reconnection attempts.
6. **Metrics**: Enrichment logging should include: total_nodes, hover_ok, hover_err,
   hover_nil, type_empty, enriched, reconnect_attempts (if any).
7. **No regression**: Existing enrichment behavior (single-provider case, small repos)
   must continue to work correctly.

---

## Testing Strategy

1. **Unit tests**: Mock the LSP client transport. Verify that:
   - `maxParallel` limits concurrent goroutines.
   - Every `didOpen` is paired with `didClose`.
   - Server-exit errors trigger reconnection.
   - Concurrent goroutines don't double-reconnect.

2. **Integration test**: Run enrichment against a medium-size project (~10K nodes).
   Verify enriched ratio improves from ~1.5% to >50%.

3. **Stress test**: Run enrichment against a large project (200K+ nodes). Verify
   the language server process does not OOM and enrichment completes successfully.
