package claudecode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zzet/gortex/internal/agents"
)

func TestLegacyArtifactHashCatalogCoversShippedArtifacts(t *testing.T) {
	for name := range GlobalSkills {
		if legacyGlobalSkillHashes[name] == "" {
			t.Errorf("missing legacy skill fingerprint for %s", name)
		}
	}
	for name := range SlashCommands {
		if legacySlashCommandHashes[name] == "" {
			t.Errorf("missing legacy command fingerprint for %s", name)
		}
	}
	for name := range SubAgents {
		if legacySubAgentHashes[name] == "" {
			t.Errorf("missing legacy sub-agent fingerprint for %s", name)
		}
	}
}

func TestWriteAgentArtifactMigratesOnlyExactLegacyBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	legacy := []byte("exact old Gortex artifact\n")
	current := "new public-tool artifact\n"
	if err := os.WriteFile(path, legacy, 0o644); err != nil {
		t.Fatal(err)
	}

	action, err := writeAgentArtifact(nil, path, current, artifactHash(legacy), agents.ApplyOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if action.Action != agents.ActionMerge {
		t.Fatalf("legacy action = %s, want merge", action.Action)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != current {
		t.Fatalf("legacy artifact was not migrated: %q", got)
	}

	custom := []byte("user customized policy\n")
	if err := os.WriteFile(path, custom, 0o644); err != nil {
		t.Fatal(err)
	}
	action, err = writeAgentArtifact(nil, path, current, artifactHash(legacy), agents.ApplyOpts{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if action.Action != agents.ActionSkip || action.Reason != "customised" {
		t.Fatalf("custom action = %+v, want customized skip", action)
	}
	got, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(custom) {
		t.Fatalf("custom artifact was overwritten: %q", got)
	}
}
