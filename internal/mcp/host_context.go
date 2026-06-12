package mcp

import (
	"context"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// hostContext is a runtime, per-host adaptation of the served tool
// surface, resolved from the MCP initialize clientInfo.name. It is the
// serve-time counterpart of the install-time agent adapters: it can hide
// tools the host duplicates, override individual tool descriptions, and
// carry a host-specific guidance fragment surfaced via tool_profile.
type hostContext struct {
	name         string            // canonical host name ("" = no context)
	matches      []string          // lowercase substrings of clientInfo.name that select this context
	instruction  string            // host-specific guidance fragment
	excluded     map[string]bool   // tools removed from this host's tools/list
	descOverride map[string]string // per-tool description replacements
}

// empty reports whether this context applies no adaptation at all.
func (h hostContext) empty() bool {
	return h.name == "" && len(h.excluded) == 0 && len(h.descOverride) == 0
}

// apply returns tools with this context's exclusions removed and its
// description overrides applied. The input slice and its elements are
// not mutated.
func (h hostContext) apply(tools []mcp.Tool) []mcp.Tool {
	if len(h.excluded) == 0 && len(h.descOverride) == 0 {
		return tools
	}
	out := make([]mcp.Tool, 0, len(tools))
	for _, t := range tools {
		if h.excluded[t.Name] {
			continue
		}
		if ov, ok := h.descOverride[t.Name]; ok {
			t.Description = ov
		}
		out = append(out, t)
	}
	return out
}

// editorHostInstruction is shared by the IDE-extension hosts.
const editorHostInstruction = "You are driving Gortex from an editor extension. Push unsaved buffers " +
	"with overlay_push so graph queries see your in-progress edits before they reach disk, and use " +
	"preview_edit / simulate_chain to evaluate a change without writing it."

// hostContexts is the runtime registry of per-host adaptations, matched
// against the MCP initialize clientInfo.name. Order matters — the first
// matching entry wins, so more specific hosts come first.
var hostContexts = []hostContext{
	{
		name:    "claude-code",
		matches: []string{"claude"},
		instruction: "Gortex runs here with PreToolUse hooks that redirect Read / Grep / Glob to graph " +
			"tools. Begin every task with smart_context, prefer get_symbol_source over reading whole " +
			"files, and edit through edit_file / edit_symbol.",
	},
	{name: "cursor", matches: []string{"cursor"}, instruction: editorHostInstruction},
	{name: "vscode", matches: []string{"vscode", "visual studio"}, instruction: editorHostInstruction},
	{name: "zed", matches: []string{"zed"}, instruction: editorHostInstruction},
	{name: "windsurf", matches: []string{"windsurf"}, instruction: editorHostInstruction},
	{name: "jetbrains", matches: []string{"jetbrains", "intellij"}, instruction: editorHostInstruction},
	{
		name:    "codex",
		matches: []string{"codex"},
		instruction: "Gortex is available as an MCP server. Use search_symbols and smart_context to " +
			"locate code before editing, and verify changes with check_guards and get_test_targets.",
	},
}

// resolveHostContext returns the hostContext matching clientName, or the
// zero context (no adaptation) when the host is unknown or unidentified.
func resolveHostContext(clientName string) hostContext {
	name := strings.ToLower(strings.TrimSpace(clientName))
	if name == "" {
		return hostContext{}
	}
	for _, hc := range hostContexts {
		for _, m := range hc.matches {
			if strings.Contains(name, m) {
				return hc
			}
		}
	}
	return hostContext{}
}

// sessionHostContext resolves the per-host context for the request's
// session from the MCP client name captured at initialize time.
func (s *Server) sessionHostContext(ctx context.Context) hostContext {
	sess := s.sessionFor(ctx)
	if sess == nil {
		return hostContext{}
	}
	return resolveHostContext(sess.snapshotClientName())
}
