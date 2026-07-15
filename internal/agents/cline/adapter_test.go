package cline

import (
	"slices"
	"testing"

	"github.com/zzet/gortex/internal/agents"
)

func TestAlwaysAllowUsesSafeCompactSurface(t *testing.T) {
	want := agents.CompactMCPAutoApproveTools()
	if !slices.Equal(alwaysAllow, want) {
		t.Fatalf("alwaysAllow=%v want safe compact tools %v", alwaysAllow, want)
	}
}
