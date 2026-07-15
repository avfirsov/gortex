package claudecode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zzet/gortex/internal/agents"
)

func TestV060ArtifactHashCatalogCoversShippedArtifacts(t *testing.T) {
	for name := range GlobalSkills {
		if v060GlobalSkillHashes[name] == "" {
			t.Errorf("missing v0.60.0 skill fingerprint for %s", name)
		}
	}
	for name := range SlashCommands {
		if v060SlashCommandHashes[name] == "" {
			t.Errorf("missing v0.60.0 command fingerprint for %s", name)
		}
	}
	for name := range SubAgents {
		if v060SubAgentHashes[name] == "" {
			t.Errorf("missing v0.60.0 sub-agent fingerprint for %s", name)
		}
	}
}

func TestWriteAgentArtifactMigratesOnlyExactV060Bytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	v060 := []byte("exact old Gortex artifact\n")
	current := "new public-tool artifact\n"
	if err := os.WriteFile(path, v060, 0o644); err != nil {
		t.Fatal(err)
	}

	action, err := writeAgentArtifact(nil, path, current, artifactHash(v060), agents.ApplyOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if action.Action != agents.ActionMerge {
		t.Fatalf("v0.60.0 action = %s, want merge", action.Action)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != current {
		t.Fatalf("v0.60.0 artifact was not migrated: %q", got)
	}

	custom := []byte("user customized policy\n")
	if err := os.WriteFile(path, custom, 0o644); err != nil {
		t.Fatal(err)
	}
	action, err = writeAgentArtifact(nil, path, current, artifactHash(v060), agents.ApplyOpts{Force: true})
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
