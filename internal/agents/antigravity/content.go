// Package antigravity implements the Gortex init integration for
// Google's Antigravity. We register native MCP and write a Knowledge Item at
// ~/.gemini/antigravity/knowledge/gortex-workflow/ — the official
// mechanism for teaching Antigravity the mandatory public-tool workflow.
package antigravity

import "github.com/zzet/gortex/internal/agents"

// Metadata is the KI manifest. Antigravity reads it to show the KI in its UI
// and locate the public-tool workflow. The summary is deliberately transport
// specific: this adapter registers native MCP, so missing handles are an
// integration failure rather than permission to change transports.
const Metadata = `{
  "summary": "MANDATORY: Use native Gortex MCP tools for indexed code. If configured tools are missing, report an MCP integration failure; do not start a daemon or use a CLI fallback.",
  "references": ["artifacts/gortex-instructions.md"]
}
`

// Instructions reuses the same compact, agent-neutral workflow installed by
// every other MCP adapter. Antigravity's native MCP registration makes direct
// tool calls mandatory.
const Instructions = `---
type: "Knowledge Item"
description: "Mandatory Gortex public-tool workflow"
---

` + agents.InstructionsBody

// These exact artifacts shipped in gortex v0.60.0. They remain solely as
// byte-for-byte migration fingerprints; customized KI files never match them
// and are therefore preserved. The retirement gate is in docs/versioning.md.
const v060Metadata = `{
  "summary": "MANDATORY: Instructions on how to use the local gortex engine CLI to significantly improve codebase intelligence. Antigravity must use run_command with gortex query over standard file read commands.",
  "references": ["artifacts/gortex-instructions.md"]
}
`

// v060Instructions is the v0.60.0 CLI-first KI body.
const v060Instructions = `---
type: "Knowledge Item"
description: "Gortex Workflow and Tools for Antigravity"
---

# Gortex Engine Usage

This codebase is indexed by Gortex, an in-memory knowledge graph code intelligence engine.

**MANDATORY:** You MUST prioritize using the Gortex CLI via ` + "`run_command`" + ` instead of using standard text searches (` + "`grep_search`" + `) or full file reads (` + "`view_file`" + `). This drastically improves token efficiency and prevents hallucination.

## Setup
If the daemon is not tracking your target repos, register the current repo so the graph tools activate:
` + "```bash" + `
gortex track .
` + "```" + `

## Standard Workflow Translation

| Instead of... | You MUST use... (via ` + "`run_command`" + `) |
|---|---|
| ` + "`grep_search`" + ` to find a class or function | ` + "`./gortex query symbol <name> --format text`" + ` (AST-aware search) |
| ` + "`grep_search`" + ` to find all references | ` + "`./gortex query usages <id>`" + ` (zero false positives) |
| ` + "`view_file`" + ` to read a whole file to find a method | ` + "`./gortex query symbol <name>`" + ` or ` + "`./gortex query callers <func_id>`" + ` |
| Guessing what breaks during a refactor | ` + "`./gortex query dependents <id>`" + ` (impact analysis) |
| Creating circular dependencies | Evaluate ` + "`./gortex query deps <id>`" + ` first |

## Example Usage

### 1. View Architecture and Communities
` + "```bash" + `
./gortex query stats
` + "```" + `

### 2. Find specific symbol definition
` + "```bash" + `
./gortex query symbol MyController
` + "```" + `

### 3. Trace blast radius
If you are modifying ` + "`core/parser.go::Parse`" + `, check what will break:
` + "```bash" + `
./gortex query dependents core/parser.go::Parse --depth 2
` + "```" + `

This gives you perfectly accurate AST-level analysis, guaranteeing safe edits.
`
