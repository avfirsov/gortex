package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/agents"
	"github.com/zzet/gortex/internal/agents/claudecode"
	"github.com/zzet/gortex/internal/agents/codex"
	"github.com/zzet/gortex/internal/agents/copilotcli"
	"github.com/zzet/gortex/internal/agents/opencode"
	"github.com/zzet/gortex/internal/agents/pi"
)

func TestSkillsStageLabel(t *testing.T) {
	piAdapter := pi.New()
	ccAdapter := claudecode.New()

	t.Run("routing-only adapters never claim skill files", func(t *testing.T) {
		label := skillsStageLabel(21, []agents.Adapter{piAdapter})
		assert.Contains(t, label, "communities block(s)")
		assert.Contains(t, label, "no skill files")
		assert.NotContains(t, label, "21 community skill")
	})

	t.Run("skill-file adapters keep the skill wording", func(t *testing.T) {
		label := skillsStageLabel(3, []agents.Adapter{ccAdapter})
		assert.Equal(t, "3 community skill(s)", label)
	})

	t.Run("mixed selection reports both mechanisms", func(t *testing.T) {
		label := skillsStageLabel(3, []agents.Adapter{ccAdapter, piAdapter})
		assert.Contains(t, label, "3 community skill(s)")
		assert.Contains(t, label, "communities block(s) in 1 instruction file(s)")
	})

	t.Run("empty selection says so", func(t *testing.T) {
		label := skillsStageLabel(3, nil)
		assert.Contains(t, label, "no adapter selected")
	})
}

// TestSkillFilesWriterSet is a drift fence: the stage summary only claims
// skill files for adapters that actually write them. If a new adapter
// starts installing generated SKILL.md files it must be added here; if
// one stops, removing it here keeps the summary honest.
func TestSkillFilesWriterSet(t *testing.T) {
	var writers, routingOnly []string
	for _, a := range buildRegistry().All() {
		if w, ok := a.(agents.SkillFilesWriter); ok && w.WritesSkillFiles() {
			writers = append(writers, a.Name())
		} else {
			routingOnly = append(routingOnly, a.Name())
		}
	}

	assert.ElementsMatch(t, []string{
		claudecode.Name, codex.Name, copilotcli.Name, opencode.Name,
	}, writers, "adapters claiming generated skill files drifted — update skillsStageLabel expectations")

	// The issue-#4 reporter: Pi must never claim skill files.
	assert.Contains(t, routingOnly, "pi")
	require.Len(t, writers, 4, "unexpected extra skill-file adapters: %s", strings.Join(writers, ", "))
}
