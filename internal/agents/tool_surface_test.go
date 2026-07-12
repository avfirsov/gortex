package agents

import (
	"slices"
	"testing"
)

func TestCompactMCPAutoApproveTools(t *testing.T) {
	tools := CompactMCPAutoApproveTools()
	if len(tools) != 11 {
		t.Fatalf("auto-approved compact tools = %d, want 11", len(tools))
	}
	for _, required := range []string{"analyze", "capabilities", "change", "explore", "read", "recall", "relations", "response", "search", "trace", "workspace"} {
		if !slices.Contains(tools, required) {
			t.Errorf("auto-approved compact tools missing %q", required)
		}
	}
	for _, approvalGated := range []string{
		"ask", "edit", "overlay", "pr", "publish_review", "refactor",
		"remember", "review", "session", "workspace_admin",
	} {
		if slices.Contains(tools, approvalGated) {
			t.Errorf("effectful or open-world facade %q must require explicit approval", approvalGated)
		}
	}

	tools[0] = "mutated"
	if slices.Contains(CompactMCPAutoApproveTools(), "mutated") {
		t.Error("CompactMCPAutoApproveTools must return a defensive copy")
	}
}

func TestUpsertMCPServerApprovalListMigratesOnlyApprovalField(t *testing.T) {
	root := map[string]any{"mcpServers": map[string]any{
		"gortex": map[string]any{
			"command":     "gortex",
			"args":        []any{"mcp"},
			"alwaysAllow": []any{"search_symbols", "read_file"},
			"env":         map[string]any{"CUSTOM": "keep"},
		},
	}}
	entry := DefaultGortexMCPEntry()
	entry["alwaysAllow"] = CompactMCPAutoApproveTools()
	legacy := []string{"search_symbols", "read_file"}
	if !UpsertMCPServerApprovalList(root, "gortex", "alwaysAllow", CompactMCPAutoApproveTools(), entry, ApplyOpts{}, legacy) {
		t.Fatal("legacy approval list should migrate")
	}
	got := root["mcpServers"].(map[string]any)["gortex"].(map[string]any)
	if !stringListEqual(got["alwaysAllow"], CompactMCPAutoApproveTools()) {
		t.Fatalf("alwaysAllow=%v", got["alwaysAllow"])
	}
	if got["env"].(map[string]any)["CUSTOM"] != "keep" {
		t.Fatalf("migration clobbered custom entry fields: %v", got)
	}
	if UpsertMCPServerApprovalList(root, "gortex", "alwaysAllow", CompactMCPAutoApproveTools(), entry, ApplyOpts{}, legacy) {
		t.Fatal("current approval list should be idempotent")
	}
}

func TestUpsertMCPServerApprovalListPreservesCustomNarrowPolicy(t *testing.T) {
	root := map[string]any{"mcpServers": map[string]any{
		"gortex": map[string]any{
			"command":     "gortex",
			"args":        []any{"mcp"},
			"alwaysAllow": []any{"read"},
		},
	}}
	entry := DefaultGortexMCPEntry()
	entry["alwaysAllow"] = CompactMCPAutoApproveTools()
	legacy := []string{"search_symbols", "read_file"}
	if UpsertMCPServerApprovalList(root, "gortex", "alwaysAllow", CompactMCPAutoApproveTools(), entry, ApplyOpts{}, legacy) {
		t.Fatal("custom approval policy must not be widened")
	}
	got := root["mcpServers"].(map[string]any)["gortex"].(map[string]any)["alwaysAllow"]
	list, ok := got.([]any)
	if !ok || len(list) != 1 || list[0] != "read" {
		t.Fatalf("custom approval policy changed: %#v", got)
	}
}
