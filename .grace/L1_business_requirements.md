# Business Requirements — L1: gortex → Understand-Anything exporter

> GRACE task spec for `/develop`. Target: `/mnt/d/code/gortex` (this repo, branch `gortex-ua-bridge`).
> Self-contained (zero-context survival). Canonical/extended copy: `/mnt/d/code/project-gortex-ua-bridge/specs/L1-gortex-ua-exporter.md`.

## 1. PROBLEM / GOAL
Add a **deterministic** exporter that renders gortex's in-memory code graph into the
**Understand-Anything** format (`understand-anything@1`, file `.understand-anything/knowledge-graph.json`)
and a minimal `generic@1` (`{nodes, edges}`) fallback. No LLM. This lets a gortex graph be loaded
into the UA dashboard and published to the `looptech-ai/understand-quickly` registry.

## 2. WHY (rationale)
gortex already has `internal/exporter/{cypher,graphml,mermaid}.go`. This is one more format renderer
of the same graph. The UA schema is `zod.passthrough()`, so extra gortex fields do not break validation
and survive round-trips (foundation for L3). `Edge.Confidence` maps directly to UA `weight`. The mapping
is pure (Calculation), fully unit-testable.

## 3. SOURCE MODEL (gortex — already exists)
- `internal/graph/node.go`: `Node{ ID, Kind (NodeKind), Name, QualName, FilePath, StartLine, EndLine, Language, Meta map[string]any, RepoPrefix, WorkspaceID }`
- `internal/graph/edge.go`: `Edge{ From, To, Kind (EdgeKind), FilePath, Line, Confidence float64, ConfidenceLabel, Origin, Tier, IdentityHash }`
- `graph.Store` exposes nodes/edges for traversal (see existing exporters for the access pattern).

## 4. TARGET SCHEMA (Understand-Anything — `packages/core/src/schema.ts`)
```jsonc
KnowledgeGraph { version:string, kind?:"codebase"|"knowledge",
  project:{name,languages[],frameworks[],description,analyzedAt,gitCommitHash},
  nodes:GraphNode[], edges:GraphEdge[], layers:[], tour:[] }   // L1: layers/tour = []
GraphNode { id, type:<enum21>, name, filePath?, lineRange?:[n,n],
  summary:string/*REQUIRED*/, tags:string[]/*REQUIRED*/, complexity:"simple"|"moderate"|"complex"/*REQUIRED*/,
  languageNotes? }   // .passthrough(): add gortex_kind, repo, workspace_id
GraphEdge { source, target, type:<enum35>, direction:"forward"|"backward"|"bidirectional",
  description?, weight:number[0..1] }   // .passthrough(): add gortex_kind, confidence_label, origin, tier, cross_repo
```
UA node types: `file function class module concept config document service table endpoint pipeline schema resource domain flow step article entity topic claim source`
UA edge types: `imports exports contains inherits implements calls subscribes publishes middleware reads_from writes_to transforms validates depends_on tested_by configures related similar_to deploys serves provisions triggers migrates documents routes defines_schema contains_flow flow_step cross_domain cites contradicts builds_on exemplifies categorized_under authored_by`

## 5. NODE MAPPING (gortex NodeKind → UA type)
Allowlist (exported by default): file→file; function,method→function; type,interface→class;
module,import→module; contract,route→endpoint; table,migration→table; config_key,flag→config;
resource,image,kustomization→resource; event,enum,constant→concept.
Denylist (drop by default; `--ua-granularity=full` keeps as `concept`): param, local, builtin, closure,
generic_param, enum_member, variable, column. Unknown kind → `concept` + passthrough `gortex_kind`.
Fields: id=Node.ID; name=Node.Name; filePath=Node.FilePath; lineRange=[StartLine,EndLine] if EndLine>0;
summary=QualName||Name; tags=nonEmpty([Language, string(Kind)]);
complexity = simple if outdeg<4 && (EndLine-StartLine)<40; complex if outdeg>20 || span>300; else moderate;
passthrough: gortex_kind, repo=RepoPrefix, workspace_id (if set).

## 6. EDGE MAPPING (gortex EdgeKind → UA type)
calls→calls; imports→imports; implements→implements; extends,overrides→inherits;
defines,renders_child,workspace_member→contains; member_of→contains (**swap source/target**);
references→related; similar_to→similar_to; semantically_related→related;
reads,reads_config,reads_col,queries→reads_from; writes,writes_config,writes_col→writes_to;
value_flow,arg_of,returns_to→transforms (drop unless granularity=full);
sends,emits→publishes; recvs→subscribes; spawns→triggers;
depends_on,depends_on_module,instantiates,typed_as→depends_on; tests,covered_by→tested_by;
handles_route→routes; models_table→defines_schema; configures,uses_env,toggles_flag→configures;
mounts,exposes,provides,consumes→serves; annotated→documents;
cross_repo_calls,cross_repo_implements,cross_repo_extends→cross_domain (+passthrough cross_repo=true).
Unknown → depends_on + passthrough gortex_kind. Edges whose endpoints were dropped → drop (referential integrity).
Fields: source/target=From/To (swap for member_of); weight=clamp(Confidence,0,1) (0→0.5); direction=forward;
description=Origin?; passthrough: gortex_kind, confidence_label, tier.

## 7. PROJECT META
name=basename(root) or --project-name; languages=distinct Node.Language; frameworks=gortex detect or [];
description="Exported from gortex code graph"; analyzedAt=RFC3339 (Action input, NOT time.Now() in pure fn);
gitCommitHash=target commit (Action).

## 8. ENTRY POINTS
1. Calculation (pure, no I/O, no time): `internal/exporter/understand.go` —
   `func ToUnderstandAnything(g graph.Store, opts UAOptions) (UAGraph, []Dropped)`. Mapping tables = package-level immutable maps. Stable sort by id.
2. Action wrapper: write `.understand-anything/knowledge-graph.json`; stamp analyzedAt, gitCommitHash.
3. CLI: `gortex export understand [--out PATH] [--granularity slim|full] [--generic]`.
4. MCP/HTTP: tool `export_understand`; `POST /v1/tools/export_understand`.

## 9. LOGS (log-first)
Each Action logs: start (repo, granularity), nodes in/out, edges in/out, dropped counts by reason
(denylist / dangling), output path, duration. Match existing gortex exporter log format.

## 10. ACCEPTANCE CRITERIA
1. `ToUnderstandAnything` is pure & deterministic (identical graph → byte-identical output; stable sort).
2. Output passes UA `validateGraph()` with **zero** dropped/fatal issues on gortex self-index.
3. `generic@1` mode produces valid `{nodes,edges}`.
4. Every gortex NodeKind & EdgeKind has explicit mapping or deliberate drop (enum-coverage test).
5. weight∈[0,1]; lineRange correct; referential integrity (no edges to missing nodes).
6. passthrough fields present and survive `validateGraph` (L3 foundation).

## 11. TEST PLAN (strict TDD — Go testing package)
- **Unit (pure):** mapNodeKind, mapEdgeKind, complexityOf, weightOf, tagsOf, member_of-swap; enum-coverage completeness.
- **Integration:** golden-file — index a fixture → export → compare to committed golden JSON AND run UA `validateGraph`
  (invoke the UA validator via `node` against `/mnt/d/code/understand-anything/packages/core` in the test harness, or a ported check).
- **E2E:** on **grules-engine** (`/mnt/d/code/grules-engine`, Go): `gortex index <grules> && gortex export understand`
  → file is valid UA graph, opens in UA dashboard, contains key rule-engine symbols.
  (drools `/mnt/d/code/drools` is a cross-language target reserved for L2/L3.)

## 12. MUST NOT
- No LLM calls (L1 strictly deterministic).
- No silent data loss — anything not 1:1 → passthrough or `[]Dropped` with reason (logged).
- No `time.Now()`/commit inside the pure function — only via Action wrapper.
- No type-suppression (`interface{}` abuse to dodge types), no breaking existing exporters / public API.
