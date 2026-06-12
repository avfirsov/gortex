package hooks

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain ensures hook tests never write telemetry to the user's real
// ~/.cache/gortex/hook-decisions.jsonl. Tests that want to inspect the
// log redirect it again via t.Setenv to a per-test tmp file.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "gortex-hooks-test")
	if err == nil {
		_ = os.Setenv("GORTEX_HOOK_LOG", filepath.Join(dir, "hook-decisions.jsonl"))
		defer func() { _ = os.RemoveAll(dir) }()
	}
	os.Exit(m.Run())
}
