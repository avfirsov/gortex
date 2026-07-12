package agents

// compactMCPAutoApproveTools is the read-only, local subset of the compact MCP
// surface that coding-agent adapters may approve without an extra per-call
// prompt. Coarse host allowlists cannot distinguish operations, so aggregated
// mutation and open-world facades remain approval-gated.
var compactMCPAutoApproveTools = []string{
	"analyze",
	"capabilities",
	"change",
	"explore",
	"read",
	"recall",
	"relations",
	"response",
	"search",
	"trace",
	"workspace",
}

// CompactMCPAutoApproveTools returns a fresh copy so adapter-specific config
// generation cannot mutate the shared policy.
func CompactMCPAutoApproveTools() []string {
	return append([]string(nil), compactMCPAutoApproveTools...)
}

// UpsertMCPServerApprovalList installs a new MCP entry or migrates the
// approval field of an existing Gortex-authored entry in place. Updating only
// the approval field preserves user environment and launcher customizations
// while removing stale tool names after a surface change. Non-Gortex entries
// remain untouched unless force is set.
func UpsertMCPServerApprovalList(root map[string]any, serverName, field string, tools []string, entry map[string]any, opts ApplyOpts, shippedLegacy ...[]string) bool {
	servers, ok := root["mcpServers"].(map[string]any)
	if !ok {
		servers = make(map[string]any)
	}
	existing, exists := servers[serverName]
	if !exists || opts.Force {
		servers[serverName] = entry
		root["mcpServers"] = servers
		return true
	}
	if !IsGortexAuthoredMCPEntry(existing) {
		return false
	}
	m, ok := existing.(map[string]any)
	if !ok || stringListEqual(m[field], tools) {
		return false
	}
	knownShippedList := false
	for _, legacy := range shippedLegacy {
		if stringListEqual(m[field], legacy) {
			knownShippedList = true
			break
		}
	}
	if !knownShippedList {
		// A narrower or otherwise customized approval list is user policy. Do
		// not silently widen it merely because the MCP launcher is Gortex.
		return false
	}
	m[field] = append([]string(nil), tools...)
	servers[serverName] = m
	root["mcpServers"] = servers
	return true
}

func stringListEqual(got any, want []string) bool {
	switch list := got.(type) {
	case []string:
		if len(list) != len(want) {
			return false
		}
		for i := range list {
			if list[i] != want[i] {
				return false
			}
		}
		return true
	case []any:
		if len(list) != len(want) {
			return false
		}
		for i := range list {
			if value, ok := list[i].(string); !ok || value != want[i] {
				return false
			}
		}
		return true
	default:
		return false
	}
}
