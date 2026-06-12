package hermes

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/agents"
	"github.com/zzet/gortex/internal/agents/agentstest"
	yaml "gopkg.in/yaml.v3"
)

// hookEntries returns the list of hook entries under hooks[event] in a
// parsed Hermes config, or nil. Each entry is a map[string]any.
func hookEntries(t *testing.T, cfg map[string]any, event string) []map[string]any {
	t.Helper()
	hooks, ok := cfg["hooks"].(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := hooks[event].([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, e := range raw {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// gortexHookEntry returns the single gortex hook entry under
// hooks[event], failing if absent or duplicated.
func gortexHookEntry(t *testing.T, cfg map[string]any, event string) map[string]any {
	t.Helper()
	var found map[string]any
	count := 0
	for _, e := range hookEntries(t, cfg, event) {
		if cmd, _ := e["command"].(string); strings.Contains(cmd, "--agent hermes") {
			found = e
			count++
		}
	}
	if count != 1 {
		t.Fatalf("hooks[%s]: expected exactly 1 gortex entry, got %d", event, count)
	}
	return found
}

// hookEnv returns a global-mode env with hook installation enabled at
// the given posture.
func hookEnv(t *testing.T, mode string) agents.Env {
	t.Helper()
	env, _ := agentstest.NewEnv(t)
	seedHermesHome(t, env.Home)
	env.InstallHooks = true
	env.HookMode = mode
	return env
}

// TestHermesInstallsHooks is the acceptance test for issue #51:
// `gortex install --agents=hermes --hooks` writes a valid pre_tool_call
// (matcher read_file|terminal) and pre_llm_call (no matcher) entry, and
// a re-run is a pure no-op.
func TestHermesInstallsHooks(t *testing.T) {
	env := hookEnv(t, "deny")
	a := New()
	if _, err := a.Apply(env, agents.ApplyOpts{}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	cfg := agentstest.ReadYAML(t, globalConfigPath(env.Home))

	// pre_tool_call: matcher + gortex hook command + a seconds timeout.
	pt := gortexHookEntry(t, cfg, hermesPreToolEvent)
	if pt["matcher"] != hermesToolMatcher {
		t.Errorf("pre_tool_call matcher = %v, want %q", pt["matcher"], hermesToolMatcher)
	}
	cmd, _ := pt["command"].(string)
	if !strings.Contains(cmd, "hook --agent hermes") {
		t.Errorf("pre_tool_call command should invoke the hermes hook: %q", cmd)
	}
	if strings.Contains(cmd, "--mode") {
		t.Errorf("deny posture should be emitted bare (no --mode): %q", cmd)
	}
	if pt["timeout"] != hermesHookTimeoutSecs {
		t.Errorf("pre_tool_call timeout = %v, want %d", pt["timeout"], hermesHookTimeoutSecs)
	}

	// pre_llm_call: NO matcher (Hermes rejects matchers on non-tool events).
	pl := gortexHookEntry(t, cfg, hermesPreLLMEvent)
	if _, hasMatcher := pl["matcher"]; hasMatcher {
		t.Errorf("pre_llm_call must not carry a matcher: %#v", pl)
	}
	if cmd, _ := pl["command"].(string); !strings.Contains(cmd, "hook --agent hermes") {
		t.Errorf("pre_llm_call command should invoke the hermes hook: %q", cmd)
	}

	// The MCP server stanza must still be present — hooks ride the same merge.
	if _, ok := cfg["mcp_servers"].(map[string]any)["gortex"]; !ok {
		t.Error("mcp_servers.gortex lost when hooks were added")
	}

	agentstest.AssertIdempotent(t, a, env)
}

// TestHermesNoHooksSkips verifies --no-hooks leaves the hooks block
// untouched while still writing the MCP server stanza.
func TestHermesNoHooksSkips(t *testing.T) {
	env, _ := agentstest.NewEnv(t)
	seedHermesHome(t, env.Home)
	env.InstallHooks = false

	a := New()
	if _, err := a.Apply(env, agents.ApplyOpts{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	cfg := agentstest.ReadYAML(t, globalConfigPath(env.Home))
	if _, ok := cfg["hooks"]; ok {
		t.Errorf("--no-hooks should not write a hooks block: %#v", cfg["hooks"])
	}
	if _, ok := cfg["mcp_servers"].(map[string]any)["gortex"]; !ok {
		t.Error("mcp_servers.gortex should still be written under --no-hooks")
	}
}

// TestHermesHookModeSwitch verifies switching posture re-stamps the
// pre_tool_call command in place (deny → enrich adds --mode=enrich)
// without duplicating the entry.
func TestHermesHookModeSwitch(t *testing.T) {
	env := hookEnv(t, "deny")
	a := New()
	if _, err := a.Apply(env, agents.ApplyOpts{}); err != nil {
		t.Fatalf("apply deny: %v", err)
	}

	env.HookMode = "enrich"
	if _, err := a.Apply(env, agents.ApplyOpts{}); err != nil {
		t.Fatalf("apply enrich: %v", err)
	}

	cfg := agentstest.ReadYAML(t, globalConfigPath(env.Home))
	pt := gortexHookEntry(t, cfg, hermesPreToolEvent) // also asserts no duplicate
	cmd, _ := pt["command"].(string)
	if !strings.Contains(cmd, "--mode=enrich") {
		t.Errorf("posture switch should re-stamp command with --mode=enrich: %q", cmd)
	}
}

// TestHermesHooksPreserveExistingEntriesAndComments guards the
// comment-rich merge: a hand-edited config keeps its comments, its
// unrelated keys, and any non-gortex hook entries.
func TestHermesHooksPreserveExistingEntriesAndComments(t *testing.T) {
	env := hookEnv(t, "deny")
	cfgPath := globalConfigPath(env.Home)
	original := `# my hermes config
model: hermes-4 # the good one

hooks:
  pre_tool_call:
    # block dangerous shell commands
    - matcher: terminal
      command: ~/.hermes/agent-hooks/guard.sh
      timeout: 10
hooks_auto_accept: true
`
	if err := os.WriteFile(cfgPath, []byte(original), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	a := New()
	if _, err := a.Apply(env, agents.ApplyOpts{}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	out, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	got := string(out)
	for _, want := range []string{"# my hermes config", "the good one", "block dangerous shell commands"} {
		if !strings.Contains(got, want) {
			t.Errorf("lost comment %q:\n%s", want, got)
		}
	}

	cfg := agentstest.ReadYAML(t, cfgPath)
	if cfg["hooks_auto_accept"] != true {
		t.Errorf("unrelated key hooks_auto_accept clobbered: %v", cfg["hooks_auto_accept"])
	}
	// The pre-existing guard.sh hook survives alongside the gortex one.
	entries := hookEntries(t, cfg, hermesPreToolEvent)
	var hasGuard, hasGortex bool
	for _, e := range entries {
		cmd, _ := e["command"].(string)
		if strings.Contains(cmd, "guard.sh") {
			hasGuard = true
		}
		if strings.Contains(cmd, "--agent hermes") {
			hasGortex = true
		}
	}
	if !hasGuard {
		t.Error("pre-existing guard.sh hook was dropped")
	}
	if !hasGortex {
		t.Error("gortex hook was not added alongside the existing one")
	}
}

// TestHermesPlanReportsHooks verifies Plan lists the hooks key when hook
// installation is enabled (so `init doctor` reports it) and omits it
// otherwise.
func TestHermesPlanReportsHooks(t *testing.T) {
	a := New()

	withHooks := hookEnv(t, "deny")
	plan, err := a.Plan(withHooks)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !planGlobalHasKey(plan, withHooks.Home, "hooks") {
		t.Error("Plan should report the hooks key when InstallHooks is set")
	}

	noHooks, _ := agentstest.NewEnv(t)
	seedHermesHome(t, noHooks.Home)
	noHooks.InstallHooks = false
	plan, err = a.Plan(noHooks)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if planGlobalHasKey(plan, noHooks.Home, "hooks") {
		t.Error("Plan should not report the hooks key under --no-hooks")
	}
}

// planGlobalHasKey reports whether the global-config FileAction in plan
// lists key among its merge keys.
func planGlobalHasKey(plan *agents.Plan, home, key string) bool {
	target := globalConfigPath(home)
	for _, f := range plan.Files {
		if f.Path != target {
			continue
		}
		if slices.Contains(f.Keys, key) {
			return true
		}
	}
	return false
}

// TestUpsertHookEvent_RefusesNonSequence verifies that a malformed
// `hooks.pre_tool_call` scalar is left untouched rather than clobbered.
func TestUpsertHookEvent_RefusesNonSequence(t *testing.T) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte("pre_tool_call: nonsense\n"), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	hooksNode := doc.Content[0]
	if changed := upsertHookEvent(hooksNode, "pre_tool_call", "m", "cmd", false); changed {
		t.Error("non-sequence event value should be left untouched (no change)")
	}
}
