package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/agents"
	"github.com/zzet/gortex/internal/agents/claudecode"
	"github.com/zzet/gortex/internal/agents/codex"
	"github.com/zzet/gortex/internal/agents/copilotcli"
	"github.com/zzet/gortex/internal/agents/kimi"
	"github.com/zzet/gortex/internal/agents/kiro"
	"github.com/zzet/gortex/internal/agents/opencode"
	"github.com/zzet/gortex/internal/agents/pi"
)

func TestSkillsStageLabel(t *testing.T) {
	piAdapter := pi.New()
	ccAdapter := claudecode.New()
	kiroAdapter := kiro.New()
	kimiAdapter := kimi.New()

	t.Run("routing-only adapters never claim skill files", func(t *testing.T) {
		label := skillsStageLabel(21, []agents.Adapter{piAdapter})
		assert.Contains(t, label, "communities block(s)")
		assert.Contains(t, label, "no skill files")
		assert.NotContains(t, label, "21 community skill")
	})

	t.Run("skill-file adapters also declare their routing block", func(t *testing.T) {
		// Claude Code writes the SKILL.md files and carries the
		// communities block in AGENTS.md — both are claimed.
		label := skillsStageLabel(3, []agents.Adapter{ccAdapter})
		assert.Equal(t, "3 community skill(s) + communities block(s) in 1 instruction file(s)", label)
	})

	t.Run("mixed selection reports both mechanisms", func(t *testing.T) {
		label := skillsStageLabel(3, []agents.Adapter{ccAdapter, piAdapter})
		assert.Contains(t, label, "3 community skill(s)")
		assert.Contains(t, label, "communities block(s) in 2 instruction file(s)")
	})

	t.Run("adapters that consume neither are not counted as routing", func(t *testing.T) {
		// kiro takes neither skill files nor a routing block; counting it
		// as an instruction file claimed a write that never happens.
		assert.Contains(t, skillsStageLabel(3, []agents.Adapter{kiroAdapter}),
			"no selected adapter consumes them")
		// kimi next to a skill-file adapter adds no instruction file.
		label := skillsStageLabel(3, []agents.Adapter{ccAdapter, kimiAdapter})
		assert.Equal(t, "3 community skill(s) + communities block(s) in 1 instruction file(s)", label)
	})

	t.Run("empty selection says so", func(t *testing.T) {
		label := skillsStageLabel(3, nil)
		assert.Contains(t, label, "no selected adapter consumes them")
	})
}

// TestDeliveryCapabilitySets is a two-sided drift fence: the stage
// summary only claims what adapters declare. The three sets must stay
// exhaustive and disjoint — an adapter that starts writing skill files
// or a routing block changes sets here, and skillsStageLabel follows
// automatically instead of an else branch assuming it routes.
func TestDeliveryCapabilitySets(t *testing.T) {
	var skillFiles, routing, neither []string
	for _, a := range buildRegistry().All() {
		var hasSkills, hasRouting bool
		if w, ok := a.(agents.SkillFilesWriter); ok && w.WritesSkillFiles() {
			hasSkills = true
			skillFiles = append(skillFiles, a.Name())
		}
		if r, ok := a.(agents.RoutingBlockWriter); ok && r.WritesCommunitiesRouting() {
			hasRouting = true
			routing = append(routing, a.Name())
		}
		if !hasSkills && !hasRouting {
			neither = append(neither, a.Name())
		}
	}

	assert.ElementsMatch(t, []string{
		claudecode.Name, codex.Name, copilotcli.Name, opencode.Name,
	}, skillFiles, "adapters claiming generated skill files drifted")

	assert.ElementsMatch(t, []string{
		claudecode.Name, codex.Name, copilotcli.Name, opencode.Name,
		"aider", "cline", "continue", "cursor", "gemini", "kilocode",
		"pi", "vscode", "windsurf", "zed",
	}, routing, "adapters claiming the communities routing block drifted")

	assert.ElementsMatch(t, []string{
		"antigravity", "hermes", "kimi", "kiro", "oh-my-pi", "openclaw",
	}, neither, "adapters consuming neither mechanism drifted")
}

// TestSkillsStageLabelDefaultRun pins the default path: with no --agents
// filter the registry returns every registered adapter, but Apply skips
// the ones Detect() does not claim, so the label must be built from the
// detected subset. On an empty scratch env (PATH scrubbed so CLI probes
// are machine-independent) only Claude Code — whose Detect is always
// true — and antigravity detect; antigravity consumes neither mechanism,
// so the label claims exactly one instruction file and none of the
// undetected adapters' files.
func TestSkillsStageLabelDefaultRun(t *testing.T) {
	t.Setenv("PATH", "")
	selected, err := buildRegistry().Filter("", "")
	require.NoError(t, err)
	env := agents.Env{Root: t.TempDir(), Home: t.TempDir(), Mode: agents.ModeProject}

	var running []agents.Adapter
	for _, a := range selected {
		if ok, _ := a.Detect(env); ok {
			running = append(running, a)
		}
	}
	assert.Equal(t, "5 community skill(s) + communities block(s) in 1 instruction file(s)",
		skillsStageLabel(5, running))
}
