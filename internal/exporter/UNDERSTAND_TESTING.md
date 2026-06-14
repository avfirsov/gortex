# Understand-Anything exporter — testing & QA bridge

This is the QA bridge for the L1 `gortex → Understand-Anything` exporter
(`internal/exporter/understand.go`, `cmd/gortex/export_understand.go`). It tells
an independent QA agent how to run the Go tests and the authoritative Node
validation harness, and what each layer proves.

## What was built

- `internal/exporter/understand.go` — types (`UAOptions`, `UAGraph`, `UANode`,
  `UAEdge`, `Dropped`), the **pure** core `buildUAGraph` (no I/O, no clock, no
  git), the adapter `ToUnderstandAnything`, and the Action `WriteUnderstandAnything`
  (the only function that writes bytes). Mirrors the `graphml.go` idiom.
- `cmd/gortex/export_understand.go` — `gortex export understand` CLI subcommand.
  Indexes the target repo in-process, resolves `analyzedAt` (RFC3339) and
  `gitCommit` (the only place time/git are read), writes
  `.understand-anything/knowledge-graph.json`, and logs the input→output
  accounting.
- `internal/exporter/testdata/understand_golden.json` — committed golden file.
- `internal/exporter/testdata/ua_validate.mjs` — authoritative UA validation
  harness (Node).

## How to run the Go tests

```sh
# Fast path: unit + integration (golden + Go sanity) + enum-coverage +
# determinism + generic. Skips the e2e (grules) and the authoritative harness
# subtest when their externals are absent.
go test ./internal/exporter/ -short -count=1 -v

# Full path including the grules-engine e2e (AC8). ~15s.
go test ./internal/exporter/ -count=1 -timeout 480s -v

# Regenerate the golden file after an intentional mapping change:
UPDATE_GOLDEN=1 go test ./internal/exporter/ -run TestBuildUAGraph_Integration -count=1
```

Build / lint gates (all must be clean):

```sh
go build ./...
go vet ./internal/exporter/ ./cmd/gortex/
gofmt -l internal/exporter/understand.go internal/exporter/understand_test.go cmd/gortex/export_understand.go   # must print nothing
```

## Test layers and what they prove

| Test | Acceptance | Proves |
|------|-----------|--------|
| `TestMapNodeKind` / `TestMapEdgeKind` | K2/K3 | allowlist, denylist, member_of swap, unknown→concept/depends_on, cross_repo→cross_domain, transforms |
| `TestComplexityOf` / `TestWeightOf` / `TestTagsOf` / `TestLineRangeOf` | K2 | field heuristics; weight∈[0,1] with Confidence 0→0.5; non-nil tags |
| `TestEnumCoverage_NodeKinds` / `TestEnumCoverage_EdgeKinds` / `TestEnumCoverage_SliceCompleteness` | **AC3** | every gortex NodeKind (38) & EdgeKind (64) constant is explicitly handled; the `concept`/`depends_on` defaults are themselves exercised |
| `TestBuildUAGraph_Integration` | **AC1/AC4** | golden-file match; member_of swap; cross_domain + cross_repo passthrough; unknown→concept + gortex_kind; param dropped under slim (recorded in `[]Dropped`); Confidence 0→0.5 |
| `…/authoritative_validateGraph` (subtest) | **AC1** | pipes the JSON into the real UA `validateGraph`; asserts success && 0 dropped && 0 fatal — **skips with a clear reason** when Node or the UA package are absent (never fakes, never ports the schema) |
| `TestBuildUAGraph_FullGranularityKeepsDenied` | K3 | `--granularity full` re-includes denied kinds as `concept` |
| `TestBuildUAGraph_Deterministic` | **AC5** | identical input → byte-identical JSON |
| `TestWriteUnderstandAnything_Generic` | **AC2** | `generic@1` `{nodes, edges}` is valid; referential integrity; weight bounds |
| `TestExportUnderstand_E2E_Grules` | **AC8** | builds the CLI, `gortex export understand /mnt/d/code/grules-engine`, parses the file, Go-sanity-validates it, finds `GruleEngine`/`RuleEntry`/`DataContext` |

`assertUASanity` is the **always-on** Go structural oracle that runs even when
the authoritative harness skips: every node `type` ∈ the 21 UA node types,
every edge `type` ∈ the 35 UA edge types, `summary`/`tags`/`complexity`
present, `weight`∈[0,1], and every edge endpoint references an emitted node
(referential integrity → zero dangling references).

## Enabling the authoritative UA validation (AC1)

The integration and e2e tests run the **real** UA `validateGraph` via Node when
both of these are present:

1. `node` on `PATH`.
2. The UA fork's `@understand-anything/core` package at
   `/mnt/d/code/understand-anything/understand-anything-plugin/packages/core`
   (override with the `UA_CORE` env var pointing at the package dir).

To enable (fast path — `schema.ts` needs only `zod`; node v24 strips TS types
and imports `src/schema.ts` directly, so no build is required):

```sh
CORE=/mnt/d/code/understand-anything/understand-anything-plugin/packages/core
mkdir -p /tmp/zoddir && (cd /tmp/zoddir && echo '{"private":true}' > package.json && npm i zod@4.3.6 --no-save)
mkdir -p "$CORE/node_modules" && cp -r /tmp/zoddir/node_modules/zod "$CORE/node_modules/zod"
# Canonical alternative: `cd /mnt/d/code/understand-anything && pnpm install`
# then build core so dist/index.js exists (pulls the full, native, tree-sitter deps).
```

When unavailable, the harness exits with code 2 and the Go subtest `t.Skip`s
with the message
`authoritative UA validation skipped: UA core not found at <path>; run pnpm install in /mnt/d/code/understand-anything to enable`.
This is expected on a machine without the UA fork checked out — the always-on Go
sanity check still fully validates the schema shape.

## Manual smoke check

```sh
go run ./cmd/gortex export understand /mnt/d/code/grules-engine --out /tmp/kg.json --pretty
jq '.version, (.nodes|length), (.edges|length)' /tmp/kg.json
# pipe through the authoritative validator (when UA core is present):
node internal/exporter/testdata/ua_validate.mjs < /tmp/kg.json
```
