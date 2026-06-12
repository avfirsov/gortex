package claudemd

import (
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/query"
)

// generateBlock builds the CLAUDE.md block over an empty graph — enough to
// exercise the static guidance prose, which is what the calibration covers.
func generateBlock(t *testing.T) string {
	t.Helper()
	return Generate(query.NewEngine(graph.New()), 0)
}

func TestGenerate_IncludesCalibrationGuidance(t *testing.T) {
	out := generateBlock(t)

	// The aggressive graph-first mandate must still be present — calibration
	// tempers it, it does not remove it.
	if !strings.Contains(out, "## MANDATORY: Use Gortex MCP tools instead of Read/Grep/Glob") {
		t.Fatal("generated block dropped the MANDATORY graph-first section")
	}

	// The calibration subsection must be present.
	calHeader := "### Calibration: the graph narrows scope, source confirms behavior"
	if !strings.Contains(out, calHeader) {
		t.Fatalf("generated block is missing the calibration subsection %q", calHeader)
	}

	// It must name the behavior-critical categories and the compress_bodies
	// caveat — these are the actionable parts of the guidance.
	for _, want := range []string{
		"behavior-critical",
		"migrations",
		"retry",
		"fallback",
		"compatibility shims",
		"compress_bodies:true",
		"get_symbol_source",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("calibration guidance is missing expected phrase %q", want)
		}
	}
}

func TestGenerate_CalibrationOrdering(t *testing.T) {
	out := generateBlock(t)

	mandate := strings.Index(out, "## MANDATORY: Use Gortex MCP tools")
	calib := strings.Index(out, "### Calibration: the graph narrows scope")
	workflow := strings.Index(out, "## Required workflow (every task on this repo)")

	if mandate < 0 || calib < 0 || workflow < 0 {
		t.Fatalf("missing a section: mandate=%d calib=%d workflow=%d", mandate, calib, workflow)
	}
	// Calibration sits between the mandate and the workflow checklist so an
	// agent reads "graph narrows, source confirms" before the step list.
	if mandate >= calib || calib >= workflow {
		t.Errorf("calibration must appear after the mandate and before the workflow: mandate=%d calib=%d workflow=%d", mandate, calib, workflow)
	}
}
