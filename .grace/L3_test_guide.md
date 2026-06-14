# L3 Test Guide — UA→gortex write-back (`annotate_nodes`)

> For the independent QA agent. Target worktree: `/mnt/d/code/gortex-uab` (branch `gortex-ua-bridge`). Go module `github.com/zzet/gortex`, go 1.26. NO new dependencies (Go stdlib + existing testify). Go-native tests only — no pytest/Doxygen.

## What was built (slice scope)

- **T1 `internal/graph`**
  - `Store.MergeNodeMeta(id string, kv map[string]any) (changed bool, found bool)` added to the `Store` interface (`internal/graph/store.go`).
  - In-memory `*Graph.MergeNodeMeta` + pure helper `metaDelta` (`internal/graph/meta_merge.go`). The merge runs under the single shard write lock via `lockTwoWrite(id, id)`; `metaDelta` is pure (no locks/I-O) and compares with `reflect.DeepEqual`.
  - SQLite backend `*store_sqlite.Store.MergeNodeMeta` (`internal/graph/store_sqlite/store.go`): read-modify-write of the gob Meta blob under `writeMu`, re-persisted via the `INSERT OR REPLACE` node statement.
- **T2 `internal/mcp/tools_annotate.go` (new)**
  - `registerAnnotateTools()` + `handleAnnotateNodes`. Registered in `NewServer` beside `registerUnderstandTools()` (`internal/mcp/server.go`). Auto-exposed at HTTP `/v1/tools/annotate_nodes` via the shared registry.

## Inputs (tool contract)

`annotate_nodes` arguments (both are JSON strings):

- `annotations` (required): JSON array, e.g.
  ```json
  [{"id":"pkg/foo.go::Bar","ua_summary":"parses the bar","ua_tags":["parser","io"],"ua_complexity":0.7,"ua_domain":"ingest"}]
  ```
  Every `ua_*` field is optional; only present fields are merged. Keys are namespaced `ua_*` so a merge can never shadow indexer-owned Meta.
- `add_related` (optional): JSON array of pairs, e.g. `[["idA","idB",0.83]]`. Third element (score 0..1) optional, defaults to `0.5`.

Returns `{annotated, unchanged, not_found:[ids], edges_added}`.

## How to verify (commands)

```sh
cd /mnt/d/code/gortex-uab

# Gates
go build ./...
go vet ./internal/graph/ ./internal/graph/store_sqlite/ ./internal/graph/storetest/ ./internal/mcp/
gofmt -l internal/graph/meta_merge.go internal/graph/meta_merge_test.go internal/graph/store.go \
          internal/graph/store_sqlite/store.go internal/graph/storetest/storetest.go \
          internal/mcp/tools_annotate.go internal/mcp/tools_annotate_test.go internal/mcp/server.go   # expect: empty

# Required test packages (race-clean — this is the first mutating tool)
go test ./internal/graph/ ./internal/mcp/ -race -count=1

# Store conformance across BOTH backends (in-memory + sqlite)
go test ./internal/graph/storetest/ ./internal/graph/store_sqlite/ -race -count=1 -run 'Conformance/MergeNodeMeta|MergeNodeMeta' -v
```

## Acceptance mapping (where each AC is proven)

- **AC1 — metaDelta pure + MergeNodeMeta idempotent + found semantics:** `internal/graph/meta_merge_test.go` (`TestMetaDelta_*`, `TestMergeNodeMeta_MergeOverwriteIdempotent`, `TestMergeNodeMeta_UnknownIDNoPanic`, `TestMergeNodeMeta_LazyInitsNilMeta`) and `internal/graph/storetest/storetest.go` `testMergeNodeMeta` (both backends).
- **AC2 — `-race` clean under concurrency:** `TestMergeNodeMeta_ConcurrentNoRace` (16 workers × 200 iters of interleaved `MergeNodeMeta` + `AddEdge`).
- **AC3 — round-trip, structure unchanged:** `TestMergeNodeMeta_StructuralUntouched` and the MCP `TestHandleAnnotateNodes_RoundTripAndIdempotent` (asserts `GetNode().Meta["ua_summary"]` + every structural field intact).
- **AC4 — `annotate_nodes` registered + callable; summary correct; idempotent related edges:** `internal/mcp/tools_annotate_test.go` — `TestHandleAnnotateNodes_RoundTripAndIdempotent` (summary), `TestHandleAnnotateNodes_AddRelatedIdempotentEdge` (idempotent `semantically_related` edge, origin `ua_annotated`, similarity meta), `TestHandleAnnotateNodes_DefaultScore`, `TestHandleAnnotateNodes_BadInput`, `TestAnnotate_RegisteredOnNewServer` (ListTools contains `annotate_nodes`).
- **AC5 — gates + green tests + no regression:** all four packages green under `-race` (see commands above).

## Expected `[IMP:9-10]`-equivalent telemetry (Go `t.Logf` + handler log)

- Handler emits a structured zap Info line `annotate_nodes` with fields `annotated / unchanged / not_found / edges_added` (Action layer).
- Test telemetry printed before asserts:
  - `[annotate#1] summary=map[annotated:2 edges_added:0 not_found:[pkg/foo.go::Ghost] unchanged:0]`
  - `[annotate#2 idempotent] summary=map[annotated:0 ... unchanged:2]`
  - `[concurrency] nodes=32 edges=32`

## MUST-NOT checklist (confirm none violated)

- Structural node/edge data never mutated — only additive `ua_*` Meta keys + `semantically_related` edges.
- `Node.Meta` never mutated outside the shard lock — all writes go through `MergeNodeMeta`.
- Unknown ids do not crash the batch (recorded in `not_found`, loop continues).
- No type suppression, no new dependencies.
```
