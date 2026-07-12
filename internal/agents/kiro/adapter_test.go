package kiro

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/agents"
	"github.com/zzet/gortex/internal/agents/agentstest"
)

func TestKiroCreatesAllArtifactsAndIsIdempotent(t *testing.T) {
	env, _ := agentstest.NewEnv(t)
	// Sentinel: create .kiro/ so Detect returns true.
	if err := os.MkdirAll(filepath.Join(env.Root, ".kiro"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := New()

	res, err := a.Apply(env, agents.ApplyOpts{})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	// 1 mcp.json + len(SteeringFiles) + len(HookFiles).
	want := 1 + len(SteeringFiles) + len(HookFiles)
	got := 0
	for _, f := range res.Files {
		if f.Action == agents.ActionCreate {
			got++
		}
	}
	if got != want {
		t.Fatalf("expected %d creates, got %d (%v)", want, got, res.Files)
	}

	// autoApprove list is baked into the mcp.json entry — verify.
	mcp := agentstest.ReadJSON(t, filepath.Join(env.Root, ".kiro", "settings", "mcp.json"))
	servers := mcp["mcpServers"].(map[string]any)
	gortex := servers["gortex"].(map[string]any)
	approvals, ok := gortex["autoApprove"].([]any)
	if !ok || len(approvals) == 0 {
		t.Fatalf("autoApprove missing or empty: %v", gortex)
	}
	if gortex["disabled"] != false {
		t.Fatalf("disabled should be false: %v", gortex)
	}
	approved := make([]string, 0, len(approvals))
	for _, raw := range approvals {
		approved = append(approved, raw.(string))
	}
	if !slices.Equal(approved, agents.CompactMCPAutoApproveTools()) {
		t.Fatalf("autoApprove=%v want safe compact tools %v", approved, agents.CompactMCPAutoApproveTools())
	}

	for name, body := range SteeringFiles {
		for _, legacy := range []string{"smart_context", "search_symbols", "get_symbol_source", "find_usages", "get_callers", "verify_change", "read_file"} {
			if strings.Contains(body, legacy) {
				t.Errorf("steering file %s contains legacy MCP tool %q", name, legacy)
			}
		}
		if strings.Contains(body, "facade-v1") {
			t.Errorf("steering file %s exposes an implementation version", name)
		}
	}

	agentstest.AssertIdempotent(t, a, env)
}

func TestKiroMigratesLegacyOwnedInstructions(t *testing.T) {
	env, _ := agentstest.NewEnv(t)
	legacyPath := filepath.Join(env.Root, ".kiro", "steering", "gortex-workflow.md")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte("# Gortex Code Intelligence\nCall smart_context then get_editing_context.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := New().Apply(env, agents.ApplyOpts{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "smart_context") || !strings.Contains(string(got), "explore") {
		t.Fatalf("legacy steering was not migrated:\n%s", got)
	}
}
