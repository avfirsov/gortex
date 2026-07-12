// Package toolref renders references to Gortex MCP tools for guidance text so
// every hook, adapter, and CLI message names a tool the SAME way — and never
// mints the invalid bare `gortex <tool>` shell shape that led agents astray
// (an agent that sees a bare tool name in a shell context invents
// `gortex read_file <path>`, which is not a real verb). MCP-directed guidance
// says "call the `<tool>` MCP tool"; a shell fallback renders the real
// `gortex call <tool> --arg k=v` invocation, which is the ONE correct way to
// reach a tool that has no dedicated CLI verb.
package toolref

import "strings"

// cliExample maps internal/compatibility tool names used by hooks to the
// compact public call an agent should execute from Bash. Hooks may still use
// the internal handler name for their daemon probe; emitted guidance must not
// make an agent learn that implementation vocabulary.
//
// Symbol-ID placeholders render both forms — `<file>::<Name|Recv.Name>` — so
// an agent targeting a method knows the ID carries the receiver
// (`pkg/s.go::Server.Handle`), not the bare method name; the bare form only
// resolves functions and types.
var cliExample = map[string]string{
	"read_file":           `gortex call read --arg target='{"file":"<file>"}'`,
	"get_symbol_source":   `gortex call read --arg target='{"symbol":"<file>::<Name|Recv.Name>"}'`,
	"get_editing_context": `gortex call read --arg operation=editing_context --arg target='{"file":"<file>"}'`,
	"get_file_summary":    `gortex call read --arg operation=summary --arg target='{"file":"<file>"}'`,
	"get_symbol":          `gortex call read --arg target='{"symbol":"<file>::<Name|Recv.Name>"}'`,
	"search_symbols":      `gortex call search --arg operation=symbols --arg query='<name>'`,
	"search_text":         `gortex call search --arg operation=text --arg query='<text>'`,
	"find_usages":         `gortex call relations --arg operation=usages --arg target='{"symbol":"<file>::<Name|Recv.Name>"}'`,
	"get_callers":         `gortex call relations --arg operation=callers --arg target='{"symbol":"<file>::<Name|Recv.Name>"}'`,
	"smart_context":       `gortex call explore --arg operation=context --arg task='<task>'`,
	"explore":             `gortex call explore --arg task='<task>'`,
	"get_repo_outline":    `gortex call explore --arg operation=outline --arg options='{"path_prefix":"<dir>/"}'`,
	"edit_file":           `gortex call edit --arg target='{"file":"<file>"}' --arg match='<old>' --arg replacement='<new>'`,
	"edit_symbol":         `gortex call edit --arg target='{"symbol":"<id>"}' --arg match='<old>' --arg replacement='<new>'`,
	"index_repository":    `gortex call workspace_admin --arg operation=index --arg arguments='{"path":"<repo-root>"}'`,
	"reindex_repository":  `gortex call workspace_admin --arg operation=reindex --arg arguments='{"path":"<repo-root>"}'`,
}

// MCPRef renders an MCP-directed reference to a tool: "call the `read_file` MCP
// tool". Use wherever guidance assumes the agent has the Gortex MCP server
// mounted and can call the tool directly.
func MCPRef(tool string) string {
	return "call the `" + tool + "` MCP tool"
}

// CLIFallback renders the compact shell invocation for one operation, for
// example `gortex call read --arg target='{"file":"..."}'`. This is the single
// place an internal tool reference becomes agent-facing Bash — nothing else
// should hand-assemble a `gortex …` shape, so the bare-verb mistake can never
// be re-minted piecemeal.
func CLIFallback(tool string) string {
	if example := cliExample[tool]; example != "" {
		return example
	}
	return "gortex call " + tool + " --arg key=value"
}

// ConcreteCLIFallback reports whether tool has a reviewed, shell-safe compact
// invocation rather than the generic compatibility fallback.
func ConcreteCLIFallback(tool string) (string, bool) {
	example, ok := cliExample[tool]
	return example, ok
}

// FallbackLine is the standard one-line advisory appended to graph-tool
// guidance. It teaches the `gortex call <tool> --arg …` shape (with a realistic
// example for the primary tool) so an agent in a shell context — MCP unmounted
// or degraded, or one that chose Bash — never invents the invalid
// `gortex <tool>` verb. primary names the most relevant tool for the worked
// example; pass "" to emit only the generic form. The returned string ends in a
// newline so it drops straight into a bulleted guidance block.
func FallbackLine(primary string) string {
	var b strings.Builder
	b.WriteString("  - Shell only (no MCP tools)? Reach any tool above with `gortex call <tool> --arg k=v`")
	if primary != "" {
		b.WriteString(" — e.g. `" + CLIFallback(primary) + "`")
	}
	b.WriteString(". There is no bare `gortex <tool>` verb.\n")
	return b.String()
}
