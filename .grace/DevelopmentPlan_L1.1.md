$START_DOC_NAME

**PURPOSE:** Wire the existing `exporter.WriteUnderstandAnything` into the MCP tool surface as `export_understand`, auto-exposing it over HTTP `/v1/tools/export_understand`, plus a one-line correctness fix so commit-less exports stay UA-valid.
**SCOPE:** New `internal/mcp/tools_understand.go`; one registration line in `internal/mcp/server.go`; remove `omitempty` on `UAProject.GitCommitHash` in `internal/exporter/understand.go`; tests in `internal/mcp/`.
**KEYWORDS:** MCP tool registration, export_understand, HTTP auto-exposure, graph.Store handler, UAOptions, gitCommitHash required.

$START_DOCUMENT_PLAN
### Document Plan
**SECTION_GOALS:**
- GOAL Reach the L1 exporter over MCP + HTTP, mirroring export_graph => G_EXPOSE
- GOAL Keep commit-less (daemon/multi-repo) exports UA-valid => G_VALID_NOCOMMIT
**SECTION_USE_CASES:**
- USE_CASE Agent calls export_understand over MCP and gets understand-anything@1 inline => UC_MCP
- USE_CASE Client POSTs /v1/tools/export_understand and gets the same => UC_HTTP
$END_DOCUMENT_PLAN

---

## Concept (collapsed — trivial wrapper, no superposition warranted)
Mirror the existing `export_graph` tool: a `registerUnderstandTools()` + `handleExportUnderstand()` pair that builds `UAOptions` from args and calls the already-tested `WriteUnderstandAnything`. HTTP needs no code (same registry). One struct-tag fix removes a latent omitempty that would drop the schema-required `gitCommitHash` when empty.

### 1. Draft Code Graph
```xml
<DraftCodeGraph>
  <Gortex_L1_1_Info TYPE="PROJECT_INFO">
    <annotation>Expose WriteUnderstandAnything via MCP export_understand (+ auto HTTP).</annotation>
    <BusinessScenarios>
      <Scenario NAME="ExportOverMCP">Agent -> export_understand -> understand-anything@1 inline/file</Scenario>
    </BusinessScenarios>
  </Gortex_L1_1_Info>
  <tools_understand_go FILE="internal/mcp/tools_understand.go" TYPE="MCP_TOOL_MODULE">
    <registerUnderstandTools_FUNC NAME="registerUnderstandTools" TYPE="REGISTRATION">
      <annotation>s.addTool(export_understand, s.handleExportUnderstand). Called from NewServer.</annotation>
    </registerUnderstandTools_FUNC>
    <handleExportUnderstand_FUNC NAME="handleExportUnderstand" TYPE="CONTROLLER">
      <annotation>Build UAOptions from args (granularity/generic/repo/project_name), set AnalyzedAt=now + best-effort GitCommit, call WriteUnderstandAnything, return inline text or write output_path + stats.</annotation>
      <CrossLinks>
        <Link TARGET="understand_go_WriteUnderstandAnything_FUNC" TYPE="CALLS_FUNCTION" />
      </CrossLinks>
    </handleExportUnderstand_FUNC>
  </tools_understand_go>
  <server_go FILE="internal/mcp/server.go" TYPE="MCP_SERVER_MODULE">
    <annotation>Add s.registerUnderstandTools() next to s.registerExportTools() (~line 1056).</annotation>
  </server_go>
  <understand_go FILE="internal/exporter/understand.go" TYPE="EXPORT_RENDERER_MODULE">
    <annotation>Fix: UAProject.GitCommitHash json tag drops `omitempty` (UA schema requires the field; "" is valid, omission is not).</annotation>
  </understand_go>
</DraftCodeGraph>
```

### 2. Step-by-step Data Flow
1. MCP/HTTP request `export_understand` with args → `handleExportUnderstand`.
2. `g := s.graph`; nil-guard (mirror handleExportGraph).
3. Build `exporter.UAOptions{Options:{Repo:repo}, Granularity:granularity, Generic:generic, ProjectName:project_name, AnalyzedAt:nowRFC3339, GitCommit:bestEffortCommit}`.
4. `WriteUnderstandAnything(buf, g, opts)`.
5. If `output_path` set → write file, return `mcp.NewToolResultText(stats summary)`. Else → return `mcp.NewToolResultText(buf JSON)`.
6. HTTP path: `handleToolCall` finds the tool via `mcpServer.GetTool("export_understand")` — works automatically.

Mental check: empty GitCommit → with the tag fix, JSON includes `"gitCommitHash":""` → ProjectMetaSchema (z.string()) accepts it → validateGraph success. Without the fix the field is omitted → validation fatal. Fix is necessary and sufficient.

### 3. Acceptance Criteria
- [ ] export_understand registered + in the tool list (AC1).
- [ ] inline returns valid understand-anything@1; output_path writes file + stats (AC2).
- [ ] validateGraph 0 dropped/0 fatal incl. empty-commit case (AC3).
- [ ] generic param → generic@1 (AC4).
- [ ] build/vet/gofmt clean; export_graph + L1 tests unbroken; mcp+exporter packages green (AC5).

## Implementation notes (for mode-code)
- Go-profile (same as L1): Go testing, `go test ./internal/mcp/ ./internal/exporter/ -count=1`, `go build ./...`, `go vet`, `gofmt -l`. No pytest/Doxygen. Idiomatic Go-doc comments (no `# region`/`## @`), matching tools_export.go style.
- Read `internal/mcp/tools_export.go` and mirror it precisely (arg helpers, nil-guard, output_path vs inline, stats summary text format).
- Best-effort GitCommit: keep simple — if the package already has a commit/HEAD helper reachable from the server, use it for a single-repo filter; otherwise "". Do not over-engineer; the struct-tag fix makes "" safe.
- The struct-tag fix is the ONLY change to understand.go; re-run the L1 exporter tests to confirm no regression (the golden file uses a non-empty commit, so it is unaffected; add/adjust a test asserting the empty-commit field is present).

## MUST NOT
No new deps; no LLM; don't touch export_graph/CLI/L1 pure core (except the one tag fix); no type suppression.
$END_DOC_NAME
