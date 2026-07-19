package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigManagerRevisionTracksStateChanges(t *testing.T) {
	globalPath := filepath.Join(t.TempDir(), "config.yaml")
	writeManagerRevisionTestFile(t, globalPath, "repos: []\n")
	manager, err := NewConfigManager(globalPath)
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}

	repoDir := t.TempDir()
	workspacePath := filepath.Join(repoDir, ".gortex.yaml")
	writeManagerRevisionTestFile(t, workspacePath, "workspace: first\n")
	initial := manager.Revision()
	manager.LoadWorkspaceConfig("repo", repoDir)
	loaded := manager.Revision()
	if loaded <= initial {
		t.Fatalf("workspace load did not advance revision: initial=%d loaded=%d", initial, loaded)
	}

	manager.LoadWorkspaceConfig("repo", repoDir)
	if got := manager.Revision(); got != loaded {
		t.Fatalf("unchanged workspace load advanced revision: got=%d want=%d", got, loaded)
	}

	writeManagerRevisionTestFile(t, workspacePath, "workspace: second\n")
	manager.LoadWorkspaceConfig("repo", repoDir)
	changed := manager.Revision()
	if changed <= loaded {
		t.Fatalf("changed workspace load did not advance revision: loaded=%d changed=%d", loaded, changed)
	}

	if err := manager.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := manager.Revision(); got <= changed {
		t.Fatalf("reload did not advance revision: changed=%d reloaded=%d", changed, got)
	}
}

func writeManagerRevisionTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
