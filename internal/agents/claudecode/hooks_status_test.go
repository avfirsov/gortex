package claudecode

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreToolUseStatusMessageReflectsMode(t *testing.T) {
	tests := map[string]string{
		HookModeDeny:          preToolUseStatusDeny,
		HookModeEnrich:        preToolUseStatusEnrich,
		HookModeConsultUnlock: preToolUseStatusConsultUnlock,
		HookModeAdaptiveNudge: preToolUseStatusNudge,
	}
	for mode, want := range tests {
		if got := preToolUseStatusMessage(mode); got != want {
			t.Fatalf("mode %q: got %q, want %q", mode, got, want)
		}
	}
}

func TestRewriteGortexPreToolUseStatusPreservesCustomMessage(t *testing.T) {
	generated := map[string]any{
		"command":       "/opt/gortex hook --mode=enrich",
		"statusMessage": preToolUseStatusEnrich,
	}
	custom := map[string]any{
		"command":       "/opt/gortex hook --mode=enrich",
		"statusMessage": "custom user-set message",
	}
	hooks := map[string]any{
		"PreToolUse": []any{map[string]any{"hooks": []any{generated, custom}}},
	}

	if got := rewriteGortexPreToolUseStatus(hooks, HookModeDeny); got != 1 {
		t.Fatalf("rewritten count = %d, want 1", got)
	}
	if got := generated["statusMessage"]; got != preToolUseStatusDeny {
		t.Fatalf("generated message = %#v, want %q", got, preToolUseStatusDeny)
	}
	if got := custom["statusMessage"]; got != "custom user-set message" {
		t.Fatalf("custom message changed: %#v", got)
	}
}

func TestInstallHookWithModeUpdatesManagedPreToolUseStatus(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.local.json")
	t.Setenv("PATH", t.TempDir())

	if _, err := InstallHookWithMode(io.Discard, settingsPath, HookModeDeny, agentsApplyOptsZero()); err != nil {
		t.Fatal(err)
	}
	if got := installedPreToolUseStatus(t, readSettingsHooks(t, settingsPath)); got != preToolUseStatusDeny {
		t.Fatalf("deny status = %q, want %q", got, preToolUseStatusDeny)
	}

	if _, err := InstallHookWithMode(io.Discard, settingsPath, HookModeEnrich, agentsApplyOptsZero()); err != nil {
		t.Fatal(err)
	}
	if got := installedPreToolUseStatus(t, readSettingsHooks(t, settingsPath)); got != preToolUseStatusEnrich {
		t.Fatalf("enrich status = %q, want %q", got, preToolUseStatusEnrich)
	}
}

func TestPluginPreToolUseStatusMatchesDefaultDenyMode(t *testing.T) {
	if !strings.Contains(pluginHooksJSON, `"statusMessage": "`+preToolUseStatusDeny+`"`) {
		t.Fatalf("plugin PreToolUse status does not describe its default deny command")
	}
}

func installedPreToolUseStatus(t *testing.T, hooks map[string]any) string {
	t.Helper()
	entries := hooks["PreToolUse"].([]any)
	inner := entries[0].(map[string]any)["hooks"].([]any)
	status, _ := inner[0].(map[string]any)["statusMessage"].(string)
	return status
}
