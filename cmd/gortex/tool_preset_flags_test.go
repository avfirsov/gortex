package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/config"
)

func TestApplyToolPresetFlagsMarksExplicitRollback(t *testing.T) {
	cfg := config.Default()
	require.False(t, cfg.MCP.Tools.Explicit)
	applyToolPresetFlags(cfg, "core", "defer")
	require.True(t, cfg.MCP.Tools.Explicit)
	require.Equal(t, "core", cfg.MCP.Tools.Preset)
	require.Equal(t, "defer", cfg.MCP.Tools.Mode)
}
