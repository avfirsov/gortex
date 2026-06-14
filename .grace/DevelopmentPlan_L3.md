$START_DOC_NAME

**PURPOSE:** Implement the gortex-side of L3 — a safe, idempotent `MergeNodeMeta` Store method and an `annotate_nodes` MCP tool that writes UA semantics back into `Node.Meta` (and optional `semantically_related` edges), without ever mutating structural data.
**SCOPE:** `internal/graph` (Store method + pure `metaDelta`), `internal/mcp/tools_annotate.go` (tool + handler + registration). Tests under `internal/graph` and `internal/mcp` (with `-race`).
**KEYWORDS:** write-back, Node.Meta merge, sharded-lock mutation, idempotent, semantically_related, first mutating MCP tool.

$START_DOCUMENT_PLAN
### Document Plan
**SECTION_GOALS:**
- GOAL Safe idempotent Meta merge by node id under shard lock => G_MERGE
- GOAL Expose it as annotate_nodes (MCP + auto HTTP) => G_TOOL
**SECTION_USE_CASES:**
- USE_CASE UA posts per-node summaries back; gortex GetNode shows ua_summary => UC_ROUNDTRIP
$END_DOCUMENT_PLAN

---

## Concept (collapsed — write-back via a blessed Store method)
The only real design choices are (a) encapsulate locking in a new `Store.MergeNodeMeta` vs exposing private locks — chose the method (correctness + honest interface); (b) persistence scope — chose in-memory + shutdown-snapshot for v1 (cross-restart durability is a documented follow-up). No broader superposition warranted.

### 1. Draft Code Graph
```xml
<DraftCodeGraph>
  <Gortex_L3_Info TYPE="PROJECT_INFO">
    <annotation>UA→gortex write-back: merge ua_* semantics into Node.Meta + optional semantically_related edges.</annotation>
    <BusinessScenarios>
      <Scenario NAME="WriteBack">UA -> annotate_nodes(ids, ua_*) -> gortex Node.Meta carries human semantics</Scenario>
    </BusinessScenarios>
  </Gortex_L3_Info>
  <graph_pkg FILE="internal/graph/store.go" TYPE="GRAPH_STORE_MODULE">
    <MergeNodeMeta_METHOD NAME="MergeNodeMeta" TYPE="MUTATION_API">
      <annotation>Store iface + *Graph impl. Locks the node's shard (write), applies metaDelta, returns (changed, found). Idempotent: writes only differing keys.</annotation>
      <CrossLinks><Link TARGET="graph_pkg_metaDelta_FUNC" TYPE="CALLS_FUNCTION" /></CrossLinks>
    </MergeNodeMeta_METHOD>
    <metaDelta_FUNC NAME="metaDelta" TYPE="PURE_CALCULATION">
      <annotation>Pure: returns the subset of kv whose value differs from existing Meta (deep-equal compare). No locks, unit-tested.</annotation>
    </metaDelta_FUNC>
  </graph_pkg>
  <tools_annotate_go FILE="internal/mcp/tools_annotate.go" TYPE="MCP_TOOL_MODULE">
    <registerAnnotateTools_FUNC NAME="registerAnnotateTools" TYPE="REGISTRATION">
      <annotation>s.addTool(annotate_nodes, s.handleAnnotateNodes). Called from NewServer beside registerUnderstandTools.</annotation>
    </registerAnnotateTools_FUNC>
    <handleAnnotateNodes_FUNC NAME="handleAnnotateNodes" TYPE="CONTROLLER">
      <annotation>Parse annotations + add_related; per id call g.MergeNodeMeta; per pair g.AddEdge(semantically_related); return {annotated,unchanged,not_found,edges_added}.</annotation>
      <CrossLinks>
        <Link TARGET="graph_pkg_MergeNodeMeta_METHOD" TYPE="CALLS_FUNCTION" />
        <Link TARGET="graph_AddEdge" TYPE="CALLS_FUNCTION" />
      </CrossLinks>
    </handleAnnotateNodes_FUNC>
  </tools_annotate_go>
</DraftCodeGraph>
```

### 2. Step-by-step Data Flow
1. MCP/HTTP `annotate_nodes` request → `handleAnnotateNodes`; `g := s.graph` nil-guard.
2. Parse `annotations` (JSON array of `{id, ua_summary?, ua_tags?, ua_complexity?, ua_domain?}`) and optional `add_related` pairs.
3. For each annotation: build `kv` from the present `ua_*` fields → `changed, found := g.MergeNodeMeta(id, kv)`. Tally annotated/unchanged/not_found.
   - Inside `MergeNodeMeta`: take the node's shard write lock; `n := GetNode(id)`; if nil → return `(false,false)`; `delta := metaDelta(n.Meta, kv)`; if empty → return `(false,true)`; else lazily init `n.Meta`, apply delta, return `(true,true)`. Release lock.
4. For each related pair `[a,b,score?]`: `g.AddEdge(&Edge{From:a,To:b,Kind:EdgeSemanticallyRelated,Confidence:score|0.5,Origin:"ua_annotated",Meta:{"similarity":score}})` (idempotent dedup); count.
5. Return `mcp.NewToolResultText(json{annotated, unchanged, not_found, edges_added})`. Log a structured line (Action layer).

Mental check: re-running the same batch → metaDelta empty for every id → `changed=false`, AddEdge dedup → edges_added counts only new → idempotent. Unknown id → not_found, batch continues. No structural field touched. Consistent.

### 3. Acceptance Criteria
- [ ] metaDelta pure + correct (only differing keys); MergeNodeMeta idempotent + found semantics (AC1).
- [ ] `-race` clean under concurrent MergeNodeMeta/AddEdge (AC2).
- [ ] round-trip: annotate → GetNode shows ua_summary; structure unchanged (AC3).
- [ ] annotate_nodes registered + callable (MCP + HTTP); summary correct; related edges idempotent (AC4).
- [ ] build/vet/gofmt clean; `go test ./internal/graph/ ./internal/mcp/ -race` green; no regression (AC5).

## Implementation notes (for mode-code, Go profile)
- Work in this worktree `/mnt/d/code/gortex-uab` (branch gortex-ua-bridge). Go tests (`go test ... -race`), `go build ./...`, `go vet`, `gofmt -l`. No pytest/Doxygen. Idiomatic Go-doc style, no `# region`/`## @` markup.
- Mirror `internal/mcp/tools_understand.go` for the tool registration/handler idioms; mirror `internal/graph` locking (see `lockTwoWrite`/shard pattern in graph.go) for `MergeNodeMeta`.
- Add `MergeNodeMeta` to the `Store` interface in store.go AND implement on `*Graph`. If `internal/graph/store_sqlite` (or any other Store impl) exists, implement there too OR return `(false,false)` with a logged "annotate unsupported on backend" — must compile.
- metaDelta compares values with reflect.DeepEqual (handles string/[]string/map). Keep it pure (no I/O/locks).
- Persistence: in-memory only; rides the shutdown snapshot. Add a one-line doc-comment noting cross-restart durability is out of scope for v1.

## MUST NOT
- Never mutate structural node/edge data; only `ua_*` Meta keys + `semantically_related` edges. Never mutate Meta without the shard lock (go through MergeNodeMeta). No type suppression. Unknown ids must not crash the batch. No new deps.
$END_DOC_NAME
