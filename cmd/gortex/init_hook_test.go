package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHookCommandPathIsEphemeral(t *testing.T) {
	// /bin/sh is a stable POSIX path that exists on macOS and Linux and is
	// not under any of the ephemeral roots — the right fixture for the
	// "healthy absolute path" case. (`os.Executable()` would land in
	// /private/var/folders/ under `go test`, which is itself ephemeral.)
	const existing = "/bin/sh"
	if _, err := os.Stat(existing); err != nil {
		t.Skipf("test fixture %s not present: %v", existing, err)
	}

	// An absolute path under a temp dir that we never create, so the
	// "missing absolute path" branch fires deterministically. We point at
	// /tmp directly (not t.TempDir()) so the prefix check doesn't shortcut
	// the missing-file check we want to exercise.
	missing := filepath.Join("/nonexistent-root-for-gortex-test", "ghost-binary")

	cases := []struct {
		name    string
		cmd     string
		want    bool
		comment string
	}{
		{"empty", "", false, "no fields to inspect"},
		{"bareName", "gortex hook", false, "PATH lookup happens at fire time"},
		{"relative", "./gortex hook", false, "relative paths are user choice, not ephemeral"},
		{"tmp", "/tmp/gortex-hook-fix hook", true, "/tmp is wiped between sessions"},
		{"varFolders", "/var/folders/x/y/z/gortex hook", true, "macOS go-build cache"},
		{"privateTmp", "/private/tmp/gortex hook", true, "macOS resolves /tmp via /private/tmp"},
		{"privateVarFolders", "/private/var/folders/x/y/z/gortex hook", true, "fully resolved go-build cache"},
		{"missingAbsolute", missing + " hook", true, "absolute path that no longer exists"},
		{"healthyAbsolute", existing + " hook", false, "absolute path that exists on disk"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := hookCommandPathIsEphemeral(tc.cmd)
			assert.Equal(t, tc.want, got, tc.comment)
		})
	}
}

func TestHealStaleHookCommands(t *testing.T) {
	const newCmd = "/opt/homebrew/bin/gortex hook"

	t.Run("emptyHooks", func(t *testing.T) {
		hooks := map[string]any{}
		got := healStaleHookCommands(hooks, newCmd)
		assert.Equal(t, 0, got)
	})

	t.Run("noGortexEntries", func(t *testing.T) {
		hooks := map[string]any{
			"PreToolUse": []any{
				makeHookEntry("Read", "/usr/local/bin/some-other-tool run"),
			},
		}
		got := healStaleHookCommands(hooks, newCmd)
		assert.Equal(t, 0, got)
		// Untouched.
		entries := hooks["PreToolUse"].([]any)
		inner := entries[0].(map[string]any)["hooks"].([]any)
		cmd := inner[0].(map[string]any)["command"].(string)
		assert.Equal(t, "/usr/local/bin/some-other-tool run", cmd)
	})

	t.Run("healthyGortexEntryUntouched", func(t *testing.T) {
		hooks := map[string]any{
			"PreToolUse": []any{makeHookEntry("Read", "./gortex hook")},
		}
		got := healStaleHookCommands(hooks, newCmd)
		assert.Equal(t, 0, got)
		assert.Equal(t, "./gortex hook", extractCmd(t, hooks, "PreToolUse", 0))
	})

	t.Run("staleEntryRewritten", func(t *testing.T) {
		hooks := map[string]any{
			"Stop": []any{makeHookEntry("", "/tmp/gortex-hook-fix hook")},
		}
		got := healStaleHookCommands(hooks, newCmd)
		assert.Equal(t, 1, got)
		assert.Equal(t, newCmd, extractCmd(t, hooks, "Stop", 0))
	})

	t.Run("multipleEventsAndMixed", func(t *testing.T) {
		hooks := map[string]any{
			"PreToolUse": []any{
				makeHookEntry("Read", "./gortex hook"),                 // healthy gortex — leave
				makeHookEntry("Read", "/usr/local/bin/lint --strict"),  // non-gortex — leave
			},
			"PreCompact": []any{makeHookEntry("", "/tmp/gortex-hook-fix hook")}, // heal
			"Stop":       []any{makeHookEntry("", "/tmp/gortex-hook-fix hook")}, // heal
		}
		got := healStaleHookCommands(hooks, newCmd)
		assert.Equal(t, 2, got)
		assert.Equal(t, "./gortex hook", extractCmd(t, hooks, "PreToolUse", 0))
		assert.Equal(t, "/usr/local/bin/lint --strict", extractCmd(t, hooks, "PreToolUse", 1))
		assert.Equal(t, newCmd, extractCmd(t, hooks, "PreCompact", 0))
		assert.Equal(t, newCmd, extractCmd(t, hooks, "Stop", 0))
	})
}

func TestResolveHookCommand(t *testing.T) {
	t.Run("foundOnPath", func(t *testing.T) {
		dir := t.TempDir()
		fake := filepath.Join(dir, "gortex")
		require.NoError(t, os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755))
		t.Setenv("PATH", dir)

		got := resolveHookCommand()
		assert.Equal(t, fake+" hook", got, "should resolve to absolute path on PATH")
	})

	t.Run("notFoundFallsBackToBare", func(t *testing.T) {
		// Empty PATH means LookPath cannot resolve "gortex" anywhere.
		t.Setenv("PATH", t.TempDir())

		got := resolveHookCommand()
		assert.Equal(t, "gortex hook", got, "fallback to bare name keeps init working in sandboxes")
	})
}

// makeHookEntry builds a single Claude Code hook entry shaped like what
// installHook writes: `{matcher?, hooks: [{type, command, ...}]}`.
func makeHookEntry(matcher, command string) map[string]any {
	entry := map[string]any{
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": command,
				"timeout": 3000,
			},
		},
	}
	if matcher != "" {
		entry["matcher"] = matcher
	}
	return entry
}

func extractCmd(t *testing.T, hooks map[string]any, event string, idx int) string {
	t.Helper()
	list, ok := hooks[event].([]any)
	require.True(t, ok, "event %q missing", event)
	require.Greater(t, len(list), idx, "event %q has fewer than %d entries", event, idx+1)
	entry, ok := list[idx].(map[string]any)
	require.True(t, ok)
	inner, ok := entry["hooks"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, inner)
	em, ok := inner[0].(map[string]any)
	require.True(t, ok)
	cmd, _ := em["command"].(string)
	return cmd
}
