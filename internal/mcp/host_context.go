package mcp

import (
	"context"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// hostContext is a runtime, per-host adaptation of the served tool
// surface, resolved from the MCP initialize clientInfo.name. It is the
// serve-time counterpart of the install-time agent adapters: it can hide
// tools the host duplicates and override individual tool descriptions.
// Workflow guidance is surface-dependent and comes from MCP initialize, not
// from host labels: a legacy tool_profile must never recommend compact names.
type hostContext struct {
	name         string            // canonical host name ("" = no context)
	matches      []string          // lowercase substrings of clientInfo.name that select this context
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

// hostContexts is the runtime registry of per-host adaptations, matched
// against the MCP initialize clientInfo.name. Order matters — the first
// matching entry wins, so more specific hosts come first.
var hostContexts = []hostContext{
	{
		name:    "claude-code",
		matches: []string{"claude-code", "claude code"},
	},
	{name: "cursor", matches: []string{"cursor"}},
	{name: "vscode", matches: []string{"vscode", "visual studio"}},
	{name: "zed", matches: []string{"zed"}},
	{name: "windsurf", matches: []string{"windsurf"}},
	{name: "jetbrains", matches: []string{"jetbrains", "intellij"}},
	{
		name:    "codex",
		matches: []string{"codex", "openai-codex"},
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
			if matchesClientAlias(name, m) {
				return hc
			}
		}
	}
	return hostContext{}
}

// matchesClientAlias accepts an exact client family or an anchored versioned
// spelling. Substring matching is unsafe here: names such as "not-codex" and
// "Claude Desktop" must remain unknown and therefore JSON-only.
func matchesClientAlias(name, alias string) bool {
	if name == alias {
		return true
	}
	for _, separator := range []string{" ", "/", "@"} {
		if strings.HasPrefix(name, alias+separator) {
			return true
		}
	}
	return false
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
