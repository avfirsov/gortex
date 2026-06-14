$START_DOC_NAME

**PURPOSE:** Implementation plan for L1 — a deterministic Go exporter that renders the gortex code graph into the Understand-Anything format (`understand-anything@1`) and a `generic@1` fallback, validated against the real Understand-Anything `validateGraph` schema.
**SCOPE:** New package file `internal/exporter/understand.go` (+ tests) reusing the existing `exporter.snapshot()` pipeline; a `gortex export understand` CLI subcommand; unit + integration (authoritative UA validation) + e2e (grules-engine) tests.
**KEYWORDS:** code-graph export, understand-anything@1, generic@1, deterministic mapping, zod validateGraph, passthrough fidelity, Go testing, gortex self-index.

$START_DOCUMENT_PLAN
### Document Plan

**SECTION_GOALS:**
- GOAL Render gortex graph to a UA-schema-valid `knowledge-graph.json` with zero dropped/fatal issues => G_VALID
- GOAL Preserve all gortex graph information (mapped, passthrough, or explicitly recorded as dropped) => G_LOSSLESS
- GOAL Keep the new code idiomatic to gortex and maintainable by future agents => G_ZEROCTX
- GOAL Keep the exporter a pure, deterministic projection of the graph => G_DETERMINISM
- GOAL Reuse the existing exporter pipeline with minimal invasiveness => G_MINIMAL

**SECTION_USE_CASES:**
- USE_CASE Developer runs `gortex export understand` and opens the result in the UA dashboard => UC_DASHBOARD
- USE_CASE CI publishes the exported graph to the understand-quickly registry => UC_REGISTRY
- USE_CASE Integration test validates exporter output against the real UA `validateGraph` => UC_AUTHORITATIVE
- USE_CASE Exporter runs on grules-engine and produces a valid graph with key rule-engine symbols => UC_E2E

$END_DOCUMENT_PLAN

---

## Collapsed Concept: A — «Authoritative round-trip»

Pure mapping `gortex graph → understand-anything@1` with rich `.passthrough()` fields. Acceptance is validated against the **real** UA `validateGraph` (invoked via `node`, skip-guarded when UA/node unavailable) plus an always-on Go structural sanity check and a committed golden file. `generic@1` is a flag-selected reduced projection. Criteria priority (authoritative for every trade-off below): **K2 (UA validity) > K3 (losslessness) > K5 (zero-context) > K1 (determinism) > K4 (minimal invasiveness)**.

Exhaustive node-kind and edge-kind mapping tables live in the companion task spec `/mnt/d/code/gortex/.grace/L1_business_requirements.md` (sections 5 and 6) — that file is the single source of truth for the per-kind tables. This plan embeds only the structural design, the tricky mappings, the data flow, and the test strategy.

---

### 1. Draft Code Graph (Pre-Code Design Artifact)

> Go adaptation of `graph-protocol`: module/file entities use the `_go` suffix, functions `_FUNC`, Go structs/types `_TYPE`. The body of `# region`-style markup is intentionally NOT used in the generated Go (Decision 2a — idiomatic gortex Go-doc style); this XML is the Architect's pre-code thinking tool only.

```xml
<DraftCodeGraph>
  <Gortex_L1_UA_Exporter_Info TYPE="PROJECT_INFO">
    <keywords>exporter, understand-anything, generic graph, deterministic mapping, zod validation, passthrough, Go</keywords>
    <terms>NodeKind, EdgeKind, KnowledgeGraph, GraphNode, GraphEdge, snapshot, Stats</terms>
    <annotation>Deterministic projection of the gortex in-memory graph into understand-anything@1 / generic@1, validated against the real UA schema.</annotation>
    <BusinessScenarios>
      <Scenario NAME="ExportForDashboard">Developer -> runs `gortex export understand` -> valid knowledge-graph.json opens in UA dashboard</Scenario>
      <Scenario NAME="AuthoritativeValidation">Integration test -> feeds output to real UA validateGraph -> asserts zero dropped/fatal</Scenario>
    </BusinessScenarios>
  </Gortex_L1_UA_Exporter_Info>

  <understand_go FILE="internal/exporter/understand.go" TYPE="EXPORT_RENDERER_MODULE">
    <annotation>UA-format renderer; sibling of cypher.go / graphml.go / mermaid.go. Reuses exporter.snapshot().</annotation>

    <UAOptions_TYPE NAME="UAOptions" TYPE="IS_TYPE_OF_MODULE">
      <annotation>Embeds base exporter.Options; adds Granularity (slim|full), Generic bool, ProjectName, AnalyzedAt, GitCommit (Action-supplied, never time.Now() in core).</annotation>
    </UAOptions_TYPE>
    <UAGraph_TYPE NAME="UAGraph" TYPE="IS_TYPE_OF_MODULE">
      <annotation>Mirrors understand-anything@1 KnowledgeGraph: version, kind, project, nodes, edges, layers (empty for L1), tour (empty for L1).</annotation>
    </UAGraph_TYPE>
    <UANode_TYPE NAME="UANode" TYPE="IS_TYPE_OF_MODULE">
      <annotation>Mirrors GraphNode incl. required summary/tags/complexity + passthrough gortex_kind/repo/workspace_id.</annotation>
    </UANode_TYPE>
    <UAEdge_TYPE NAME="UAEdge" TYPE="IS_TYPE_OF_MODULE">
      <annotation>Mirrors GraphEdge: source/target/type/direction/weight + passthrough gortex_kind/confidence_label/tier/cross_repo.</annotation>
    </UAEdge_TYPE>

    <buildUAGraph_FUNC NAME="buildUAGraph" TYPE="PURE_CALCULATION">
      <annotation>Pure core over node/edge slices. Two passes: classify+keep nodes, then map+filter edges. Returns (UAGraph, []Dropped). Testable without a Store.</annotation>
      <CrossLinks>
        <Link TARGET="understand_go_mapNodeKind_FUNC" TYPE="CALLS_FUNCTION" />
        <Link TARGET="understand_go_mapEdgeKind_FUNC" TYPE="CALLS_FUNCTION" />
        <Link TARGET="understand_go_complexityOf_FUNC" TYPE="CALLS_FUNCTION" />
        <Link TARGET="understand_go_weightOf_FUNC" TYPE="CALLS_FUNCTION" />
        <Link TARGET="understand_go_tagsOf_FUNC" TYPE="CALLS_FUNCTION" />
      </CrossLinks>
    </buildUAGraph_FUNC>
    <mapNodeKind_FUNC NAME="mapNodeKind" TYPE="PURE_CALCULATION">
      <annotation>NodeKind -> (uaType, drop, reason) using allowlist/denylist + granularity. Unknown -> concept.</annotation>
    </mapNodeKind_FUNC>
    <mapEdgeKind_FUNC NAME="mapEdgeKind" TYPE="PURE_CALCULATION">
      <annotation>EdgeKind -> (uaType, swap). member_of -> contains with swapped source/target. Unknown -> depends_on.</annotation>
    </mapEdgeKind_FUNC>
    <complexityOf_FUNC NAME="complexityOf" TYPE="PURE_CALCULATION">
      <annotation>Heuristic simple|moderate|complex from out-degree and line span.</annotation>
    </complexityOf_FUNC>
    <weightOf_FUNC NAME="weightOf" TYPE="PURE_CALCULATION">
      <annotation>clamp(Confidence,0,1); Confidence==0 -> 0.5 (neutral default).</annotation>
    </weightOf_FUNC>
    <tagsOf_FUNC NAME="tagsOf" TYPE="PURE_CALCULATION">
      <annotation>Non-empty subset of [Language, string(Kind)]; always returns a non-nil slice.</annotation>
    </tagsOf_FUNC>
    <ToUnderstandAnything_FUNC NAME="ToUnderstandAnything" TYPE="ADAPTER">
      <annotation>Calls exporter.snapshot(g, opts.Options) then buildUAGraph(). Deterministic given the store.</annotation>
      <CrossLinks>
        <Link TARGET="exporter_snapshot" TYPE="CALLS_FUNCTION" />
        <Link TARGET="understand_go_buildUAGraph_FUNC" TYPE="CALLS_FUNCTION" />
      </CrossLinks>
    </ToUnderstandAnything_FUNC>
    <WriteUnderstandAnything_FUNC NAME="WriteUnderstandAnything" TYPE="IO_ACTION">
      <annotation>Marshals UAGraph (or generic projection) to JSON, writes via countingWriter, returns Stats. Mirrors WriteGraphML signature.</annotation>
      <CrossLinks>
        <Link TARGET="understand_go_ToUnderstandAnything_FUNC" TYPE="CALLS_FUNCTION" />
      </CrossLinks>
    </WriteUnderstandAnything_FUNC>
  </understand_go>

  <export_cmd_go FILE="cmd/gortex/export_understand.go" TYPE="CLI_CONTROLLER_MODULE">
    <annotation>`gortex export understand` subcommand. Action layer: resolves AnalyzedAt (RFC3339) and GitCommit, default out `.understand-anything/knowledge-graph.json`.</annotation>
    <CrossLinks>
      <Link TARGET="understand_go_WriteUnderstandAnything_FUNC" TYPE="CALLS_FUNCTION" />
    </CrossLinks>
  </export_cmd_go>

  <understand_test_go FILE="internal/exporter/understand_test.go" TYPE="TEST_MODULE">
    <annotation>Unit (table-driven mapping + enum-coverage), integration (golden + real UA validateGraph via node, skip-guarded + Go sanity), e2e (grules-engine, short-skippable).</annotation>
    <CrossLinks>
      <Link TARGET="understand_go_buildUAGraph_FUNC" TYPE="TESTS" />
      <Link TARGET="ua_validateGraph_external" TYPE="USES_API" />
    </CrossLinks>
  </understand_test_go>

  <ProjectCrossLinks TYPE="MODULE_INTERACTIONS_OVERVIEW">
    <Link TARGET="understand_go" TYPE="REUSES(exporter.snapshot, Options, Stats, countingWriter)" />
    <Link TARGET="export_cmd_go" TYPE="ORCHESTRATES_FLOW" />
  </ProjectCrossLinks>
</DraftCodeGraph>
```

---

### 2. Step-by-step Data Flow

1. **Entry (Action, CLI `cmd/gortex/export_understand.go`):** parse flags `--out`, `--granularity slim|full` (default slim), `--generic`, `--project-name`, `--repo`, `--pretty`. Resolve `AnalyzedAt` = RFC3339 now, `GitCommit` = `git rev-parse HEAD` of the indexed root (or gortex's existing commit helper). Build `UAOptions`. Open the gortex graph/engine exactly as the other `gortex export` subcommands do.
2. **Snapshot (reuse):** call `snapshot(g, opts.Options)` → stable-sorted `nodes`, `edges`, `kept` set (existing filters, synthetic stubs, dedup already applied).
3. **Out-degree precompute (pure):** build `map[string]int` out-degree from `edges` (count by `From`) — needed by `complexityOf`.
4. **Node pass (pure, in `buildUAGraph`):** for each node → `mapNodeKind(kind, granularity)`. If `drop` → append to `[]Dropped{ID, Kind, Reason}` and skip. Else build `UANode{ID, Type, Name, FilePath, LineRange=lineRangeOf(n), Summary=QualName||Name, Tags=tagsOf(n), Complexity=complexityOf(n, outdeg), passthrough(gortex_kind, repo, workspace_id)}`. Record kept ID in a set with its uaType.
5. **Edge pass (pure):** for each edge → `mapEdgeKind(kind)` → `(uaType, swap)`. Determine `src,tgt` (swap for `member_of`). If either endpoint is not in the kept set → append to `[]Dropped{reason: "dangling: endpoint <id> not emitted"}` and skip (preserves referential integrity → zero UA drops). Else build `UAEdge{Source, Target, Type:uaType, Direction:"forward", Weight:weightOf(e), Description:e.Origin, passthrough(gortex_kind, confidence_label, tier, cross_repo)}`.
6. **Assemble (pure):** `UAGraph{Version:"1.0.0", Kind:"codebase", Project:{Name, Languages:distinct(node.Language), Frameworks:[], Description:"Exported from gortex code graph", AnalyzedAt, GitCommitHash}, Nodes, Edges, Layers:[]UALayer{}, Tour:[]UATourStep{}}`. Re-sort nodes by ID and edges by (Source, Target, Type) for determinism. `Layers`/`Tour` are non-nil empty slices so JSON emits `[]` not `null`.
7. **Marshal + write (Action, `WriteUnderstandAnything`):** if `opts.Generic` → project to `{nodes:[{id,type,name,filePath}], edges:[{source,target,type,weight}]}` (generic@1). Else marshal full `UAGraph`. Indent when `opts.Pretty`. Write via `countingWriter`; return `Stats{NodesWritten, EdgesWritten, NodesSkipped:len(dropped nodes), EdgesSkipped:len(dropped edges), BytesWritten}`. Log boundaries (see Implementation Spec → Logging).

Mental simulation check: a gortex `function` node with 3 callees and 10-line span → UA `function`, complexity `simple`, tags `[go, function]`, summary = qualified name; a `member_of` edge (method → type) → UA `contains` (type → method) with weight from confidence; a `param` node under slim granularity → dropped with reason, and any edge touching it → dropped as dangling. Result has no dangling references → UA validator drops nothing. Consistent.

---

### 3. Acceptance Criteria

- [ ] **AC1 (K2):** On `gortex` self-index, real UA `validateGraph` returns `success:true` with zero `dropped` and zero `fatal` issues. Integration test asserts this when `node` + UA are available; otherwise it `t.Skip`s with a clear message, and the always-on Go sanity check still runs.
- [ ] **AC2 (K2):** `generic@1` output is a valid `{nodes, edges}` object (Go sanity: every node has non-empty `id`/`type`/`name`; every edge `source`/`target` reference an emitted node).
- [ ] **AC3 (K3):** Enum-coverage test enumerates every `NodeKind` and `EdgeKind` constant defined in `internal/graph` and asserts each has an explicit mapping or an explicit denylist/drop entry (no silent fallthrough beyond the documented `concept`/`depends_on` defaults, and those defaults are themselves asserted).
- [ ] **AC4 (K3):** Nothing is lost silently — every non-emitted node/edge appears in `[]Dropped` with a reason; gortex-specific fields survive as passthrough on emitted entities and pass `validateGraph`.
- [ ] **AC5 (K1):** `buildUAGraph` is pure and deterministic — identical input slices produce byte-identical JSON (stable sort verified by a repeat-run test).
- [ ] **AC6 (K1/MUST-NOT):** No `time.Now()` / git calls inside `buildUAGraph` / `ToUnderstandAnything`; only the CLI/Action layer supplies `AnalyzedAt`/`GitCommit`.
- [ ] **AC7 (K4):** No breaking change to existing `Options`/`Stats`/`snapshot`/exporters or the public API; `go build ./...` and `go vet ./...` clean; `gofmt` clean.
- [ ] **AC8 (UC_E2E):** `gortex index /mnt/d/code/grules-engine && gortex export understand` produces a valid UA graph containing recognizable grule-engine symbols (for example `GruleEngine`, `RuleEntry`, `DataContext`). Implemented as a `testing.Short()`-skippable e2e Go test.

---

## Implementation Spec (for mode-code)

### File layout
- `internal/exporter/understand.go` — types + pure core + Action writer (sibling of `graphml.go`).
- `internal/exporter/understand_test.go` — unit + integration tests.
- `internal/exporter/testdata/ua_validate.mjs` — tiny Node harness importing UA `validateGraph` (reads JSON on stdin, prints `{success, issues}`); used only by the integration test.
- `internal/exporter/testdata/understand_golden.json` — committed golden file from a fixed synthetic graph.
- `cmd/gortex/export_understand.go` — `gortex export understand` subcommand (mirror an existing `gortex export <fmt>` command for flag/wiring conventions; if export subcommands live in one file, add the subcommand there instead and note it).
- `internal/exporter/testdata/e2e_understand_test.go` content may live in `understand_test.go` guarded by `testing.Short()`.

### Types
Define `UAOptions` (embed `Options`; add `Granularity string`, `Generic bool`, `ProjectName string`, `AnalyzedAt string`, `GitCommit string`), `UAGraph`, `UAProject`, `UANode`, `UAEdge`, `UALayer`, `UATourStep`, `Dropped{ID string; Kind string; Reason string}`. JSON tags must match `understand-anything@1` exactly: camelCase `filePath`, `lineRange`, `languageNotes`, `gitCommitHash`; `summary`/`tags`/`complexity` are REQUIRED so they must NOT use `omitempty`; edge `weight` must NOT use `omitempty` (0.0 is valid and required); `tags`/`layers`/`tour`/`nodes`/`edges` must serialize as `[]` (initialize to non-nil empty slices), never `null`. `lineRange` is `*[2]int` with `omitempty` (omitted when `EndLine<=0`). Passthrough fields use `omitempty`.

### Mapping (authoritative tables in business_requirements §5/§6 — embed them in code as package-level immutable maps)
- `uaNodeType map[graph.NodeKind]string`, `uaNodeDeny map[graph.NodeKind]bool`, `uaEdgeType map[graph.EdgeKind]string`.
- Tricky cases that MUST be implemented exactly: `member_of` → `contains` with **source/target swapped**; `Confidence==0` → `weight 0.5`, otherwise `clamp(Confidence,0,1)`; unknown `NodeKind` → `concept` (+ passthrough `gortex_kind`); unknown `EdgeKind` → `depends_on` (+ passthrough `gortex_kind`); cross_repo_* edge kinds → `cross_domain` + passthrough `cross_repo:true`; denylist (slim) drops `param/local/builtin/closure/generic_param/enum_member/variable/column`, and `--granularity full` re-includes them as `concept`.
- Enumerate the authoritative constant lists by reading `internal/graph` (grep the `NodeKind`/`EdgeKind` const blocks) — the enum-coverage test (AC3) iterates exactly those constants.

### Action / CLI
- `WriteUnderstandAnything(w io.Writer, g graph.Store, opts UAOptions) (Stats, error)` — exact shape of `WriteGraphML`.
- CLI resolves `AnalyzedAt`/`GitCommit` and default out path `.understand-anything/knowledge-graph.json`; creates the directory if missing; honors `--pretty`, `--granularity`, `--generic`, `--project-name`, `--repo`.

### Go-profile overrides (Decision 1a — this SUPERSEDES the Python/pytest steps in the mode-code system prompt)
- **Tests:** Go `testing` package, table-driven, file `*_test.go`. Run via `go test ./internal/exporter/ -run Understand -v` (and `-run . -count=1`). There is **no** pytest, `conftest.py`, `caplog`, `.test_counter.json`, or `python -m pytest`. The Anti-Loop principle still holds: if a fix is not converging in 2–3 iterations, STOP and return a structured Bug Report.
- **Build/lint gates:** `go build ./...`, `go vet ./...`, `gofmt -l` (must be empty) instead of Python import checks.
- **Finalization (replaces BUILD_DOXYGEN — Doxygen does not parse Go):** run `gortex index /mnt/d/code/gortex` so gortex indexes its own new code; that self-graph is the post-code architecture index for the Go target. Optionally dogfood `gortex export understand` on gortex itself as a smoke check. Do **not** create a `Doxyfile` or `doxygen_output/`.
- **No `tests/` Python tree, no `test_guide.md` in Python form** — instead leave a short `internal/exporter/UNDERSTAND_TESTING.md` describing how to run the Go tests and the node validation harness (this is the QA bridge for mode-qa).

### Style (Decision 2a)
- Match the existing gortex exporter idiom exactly: Go package/doc comments (`// WriteUnderstandAnything emits ...`), not GRACE `# region`/`## @` markup. Carry PURPOSE/RATIONALE into the doc comments (the "why"), keep functions small and pure where the design says pure. Logging at the Action layer only, using whatever logging the other `gortex export` subcommands use, with structured fields: `nodes_in`, `nodes_out`, `edges_in`, `edges_out`, `dropped_nodes`, `dropped_edges`, `out_path`, `duration`. Keep `buildUAGraph` log-free (pure).

### Test plan
- **Unit (pure, table-driven):** `mapNodeKind` (allowlist + denylist + unknown→concept + granularity), `mapEdgeKind` (incl. member_of swap + unknown→depends_on + cross_repo→cross_domain), `complexityOf`, `weightOf` (Confidence 0→0.5, clamp, mid), `tagsOf` (non-nil, empties skipped), `lineRangeOf`. **Enum-coverage** (AC3).
- **Integration:** build a fixed synthetic graph in-test (a handful of nodes/edges covering files, functions, types, a member_of, a cross_repo edge, a denylist node, an unknown kind) → `buildUAGraph` → (a) compare to committed golden JSON; (b) pipe JSON into `node internal/exporter/testdata/ua_validate.mjs` and assert `success && no dropped/fatal` — **skip with a clear message** if `node` is missing or the UA package/dist is absent; (c) always-on Go sanity (enum membership of every emitted type/edge-type against the known UA enums, required fields present, `weight∈[0,1]`, referential integrity). For the node harness prefer the UA built `dist`; if absent, attempt `npx tsx` against the TS source; if both absent, skip (b) only.
- **E2E (AC8, `testing.Short()`-skippable):** shell `gortex index /mnt/d/code/grules-engine` + `gortex export understand --out <tmp>`; assert file parses, validates (reuse the harness, skip-guarded), and contains expected grule symbols.

### MUST NOT (from business_requirements §12 — verbatim)
- No LLM calls (L1 strictly deterministic).
- No silent data loss — anything not 1:1 → passthrough or `[]Dropped` with reason (logged).
- No `time.Now()` / commit inside the pure function — only via the Action/CLI wrapper.
- No type-suppression (no `interface{}` abuse to dodge the type system), no breaking existing exporters / public API.

$END_DOC_NAME
