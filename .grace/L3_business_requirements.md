# Business Requirements — L3: UA→gortex write-back (annotate_nodes)

> GRACE task spec. Target: /mnt/d/code/gortex-uab (worktree on branch gortex-ua-bridge). Builds on L1 (passthrough ids) + L1.1 (MCP surface).
> Self-contained: the graph-mutation constraints discovered by survey are embedded.

## 1. GOAL
Close the loop: let UA write its LLM-produced semantics (plain-English summary, tags, complexity, domain) **back into gortex** by node id, merged into `Node.Meta` under namespaced keys (`ua_summary`, `ua_tags`, `ua_complexity`, `ua_domain`), optionally adding `semantically_related` edges. Result: one graph = gortex's deterministic structural spine + UA's human semantics. Exposed as a new MUTATING MCP tool `annotate_nodes` (auto-exposed over HTTP `/v1/tools/annotate_nodes`).

## 2. WHY
`Node.Meta` is a free `map[string]any`; L1 already round-trips node ids via passthrough, so UA can address gortex nodes by the same id. gortex gains a human-comprehension layer; UA gains a place to persist its semantics on the deterministic graph.

## 3. GRAPH-MUTATION CONSTRAINTS (authoritative — from survey)
- `graph.Store` (internal/graph/store.go) exposes `GetNode(id) *Node`, `GetNodesByIDs`, `AddNode`, `AddEdge`, `RemoveEdge`, `ReindexEdge` — but **no Meta-mutation method**.
- The graph is sharded (16 shards, internal/graph/graph.go) with per-shard `RWMutex`. Mutating `Node.Meta` directly without the shard write lock is a **concurrent-map panic hazard** on the live daemon. The lock helper `lockTwoWrite(idA, idB)` is **private to `*Graph`** — not reachable from the MCP handler (which holds `graph.Store`).
- → The blessed solution is a NEW Store method that encapsulates locking + idempotent compare-before-write (see §5 T1). Do NOT expose `lockTwoWrite` or mutate Meta from the handler directly.
- `AddEdge(e *Edge)` is thread-safe (locks internally) and idempotent (edgeKey dedup — proven by internal/graph/idempotency_test.go). `graph.EdgeSemanticallyRelated = "semantically_related"` exists (internal/graph/edge.go).
- Persistence: graph state persists via gob+gzip snapshot on daemon graceful shutdown (cmd/gortex/daemon_snapshot.go `saveSnapshot`). There is NO explicit per-mutation persist API. → v1 scope: annotations live in memory for the daemon session and ride the shutdown snapshot. Cross-restart durability of annotations is a documented limitation / optional follow-up (explicit snapshot trigger), NOT in this slice.
- No analysis cache reads live `Node.Meta`, so no cache invalidation is needed after a Meta merge.
- Node fields: `ID`, `Meta map[string]any`. Edge fields: `From`, `To`, `Kind`, `Confidence`, `Origin`, `Meta`.

## 4. CONTRACT (annotate_nodes)
Input: a list of annotations `[{id, ua_summary?, ua_tags?, ua_complexity?, ua_domain?}]` (carried as a JSON arg), plus optional `add_related: [[idA, idB, score?]]` for `semantically_related` edges.
Behavior: for each annotation, merge the provided `ua_*` keys into the node's `Meta` IFF different (idempotent); skip + record nodes whose id is not found (do not fail the batch). For each related pair, add a `semantically_related` edge (idempotent via AddEdge dedup). Return `{annotated, unchanged, not_found:[ids], edges_added}`.

## 5. SCOPE (feature slice — gortex Go side)
- **T1 (internal/graph):** add `MergeNodeMeta(id string, kv map[string]any) (changed bool, found bool)` to the `Store` interface and implement on `*Graph` with proper shard locking + compare-before-write idempotency. Pure delta helper `metaDelta(existing, kv map[string]any) map[string]any` (returns only keys whose value differs) — pure, unit-tested. If a non-memory backend exists (internal/graph/store_sqlite), implement there too, or return `found=false` with a logged "annotate unsupported on backend" — must not break the build.
- **T2 (internal/mcp/tools_annotate.go, new):** `registerAnnotateTools()` + `handleAnnotateNodes`, mirroring tools_understand.go/tools_export.go. Parse args, call `g.MergeNodeMeta` per id, optional `AddEdge` for related pairs, return the JSON summary. Register in NewServer beside `registerUnderstandTools()`.
- HTTP `/v1/tools/annotate_nodes` is auto-exposed (same registry) — no HTTP code.
OUT: UA-side emit step (separate, manual/atlas — see §7); explicit cross-restart persistence; domain-node materialization (optional future).

## 6. ACCEPTANCE CRITERIA
- [ ] AC1: `MergeNodeMeta` merges `ua_*` keys; re-applying identical input → `changed=false` (idempotent); unknown id → `found=false`, no panic.
- [ ] AC2: concurrent calls are safe (no map race) — verified with `-race`.
- [ ] AC3: round-trip — export a fixture graph, annotate by id, `GetNode(id).Meta["ua_summary"]` reflects it; structural nodes/edges otherwise unchanged.
- [ ] AC4: `annotate_nodes` MCP tool registered + callable (and thus HTTP-exposed); returns the `{annotated, unchanged, not_found, edges_added}` summary; related pairs add idempotent `semantically_related` edges.
- [ ] AC5: gates — `go build ./...`, `go vet`, `gofmt -l` clean; `go test ./internal/graph/ ./internal/mcp/ -race` green; no change to existing structural behavior or other tools.

## 7. UA-side (separate, manual/atlas — like L2)
After UA generates semantics, emit a step in `skills/understand/SKILL.md` that calls `annotate_nodes` back to gortex with the per-node `ua_*` keyed by the gortex node id (same ids used in L1/L2). Not part of this Go slice.

## 8. MUST NOT
- Write-back ONLY into `Meta` and `semantically_related` edges — NEVER mutate structural nodes/edges (id/kind/name/filePath/lineRange or structural edge kinds).
- Never mutate `Node.Meta` without the shard lock (use the Store method). No type suppression. Unknown ids must not crash the batch. Determinism: same input → same effect.
- Go-native tests only (no pytest/Doxygen — same Go profile as L1).
