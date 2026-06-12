package main

// PURPOSE — integration tests for the `gortex analyze` daemonless command.
// RATIONALE — verifies that the command indexes a tiny Go Temporal fixture
// in-process and returns correct JSON for the temporal_orphans kind, and that
// toggling --temporal off produces more (or equal) orphan_activity entries
// than --temporal on.
// KEYWORDS — analyze, temporal_orphans, integration, TDD

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeAnalyzeFixture creates a tiny Go Temporal fixture in dir with:
//   - workflow.go: a workflow dispatching ChargeCard activity
//   - activity.go: the ChargeCard activity definition
//   - worker.go:   registers both workflow and activity
func writeAnalyzeFixture(t *testing.T, dir string) {
	t.Helper()
	writeAnalyzeFile(t, filepath.Join(dir, "workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

func OrderWorkflow(ctx workflow.Context, id string) error {
	return workflow.ExecuteActivity(ctx, ChargeCard, id).Get(ctx, nil)
}
`)
	writeAnalyzeFile(t, filepath.Join(dir, "activity.go"), `package wf

import "context"

func ChargeCard(ctx context.Context, id string) error {
	return nil
}
`)
	writeAnalyzeFile(t, filepath.Join(dir, "worker.go"), `package wf

func setupWorker(w Worker) {
	w.RegisterWorkflow(OrderWorkflow)
	w.RegisterActivity(ChargeCard)
}
`)
}

// writeAnalyzeFile writes content to path, creating parent dirs as needed.
func writeAnalyzeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// runAnalyzeCmd invokes analyzeCmd.RunE in-process with the given flags and
// returns the captured stdout output.
func runAnalyzeCmd(t *testing.T, kind, path, temporal, format string) string {
	t.Helper()

	// Save and restore flag values to avoid cross-test contamination.
	prevKind := analyzeKind
	prevPath := analyzePath
	prevTemporal := analyzeTemporal
	prevFormat := analyzeFormat
	t.Cleanup(func() {
		analyzeKind = prevKind
		analyzePath = prevPath
		analyzeTemporal = prevTemporal
		analyzeFormat = prevFormat
	})

	analyzeKind = kind
	analyzePath = path
	analyzeTemporal = temporal
	analyzeFormat = format

	var buf bytes.Buffer
	analyzeCmd.SetOut(&buf)
	analyzeCmd.SetErr(&bytes.Buffer{}) // silence stderr

	err := analyzeCmd.RunE(analyzeCmd, nil)
	require.NoError(t, err)

	return buf.String()
}

// TestAnalyzeCommand_TemporalOrphans_ONOFF runs the analyze command over a
// fixture with one registered but dispatched activity and checks:
//  1. --temporal on  → valid JSON with expected fields
//  2. --temporal off → orphan_activity count >= count from --temporal on
//     (when synthesis is off, the dispatch edge is not wired, so ChargeCard
//     appears as orphaned)
func TestAnalyzeCommand_TemporalOrphans_ONOFF(t *testing.T) {
	dir := t.TempDir()
	writeAnalyzeFixture(t, dir)

	// Run with temporal ON.
	outOn := runAnalyzeCmd(t, "temporal_orphans", dir, "on", "json")

	var resultOn map[string]any
	require.NoError(t, json.Unmarshal([]byte(outOn), &resultOn), "on-output must be valid JSON")

	// Verify expected top-level keys are present.
	for _, key := range []string{"broken_dispatch", "signal_no_handler", "query_no_handler", "orphan_activity", "orphan_workflow", "totals"} {
		assert.Contains(t, resultOn, key, "JSON must contain key %q", key)
	}

	// Run with temporal OFF.
	outOff := runAnalyzeCmd(t, "temporal_orphans", dir, "off", "json")

	var resultOff map[string]any
	require.NoError(t, json.Unmarshal([]byte(outOff), &resultOff), "off-output must be valid JSON")

	// When temporal synthesis is off the dispatch edge from OrderWorkflow →
	// ChargeCard is not minted, so ChargeCard is never consumed and shows up
	// as an orphan. When temporal synthesis is on the edge exists and
	// ChargeCard is consumed → not an orphan. Therefore:
	//   len(orphan_activity OFF) >= len(orphan_activity ON)
	countOrphans := func(result map[string]any) int {
		raw, ok := result["orphan_activity"]
		if !ok {
			return 0
		}
		slice, ok := raw.([]any)
		if !ok {
			return 0
		}
		return len(slice)
	}

	orphansOn := countOrphans(resultOn)
	orphansOff := countOrphans(resultOff)
	assert.GreaterOrEqual(t, orphansOff, orphansOn,
		"orphan_activity count with temporal off (%d) should be >= count with temporal on (%d)",
		orphansOff, orphansOn)
}

// TestAnalyzeCommand_TextFormat ensures --format text runs without error and
// produces non-empty human-readable output.
func TestAnalyzeCommand_TextFormat(t *testing.T) {
	dir := t.TempDir()
	writeAnalyzeFixture(t, dir)

	out := runAnalyzeCmd(t, "temporal_orphans", dir, "on", "text")
	assert.NotEmpty(t, out, "text output must be non-empty")
}

// TestAnalyzeCommand_Synthesizers ensures the synthesizers kind returns valid JSON.
func TestAnalyzeCommand_Synthesizers(t *testing.T) {
	dir := t.TempDir()
	writeAnalyzeFixture(t, dir)

	out := runAnalyzeCmd(t, "synthesizers", dir, "on", "json")
	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.Contains(t, result, "synthesizers")
	assert.Contains(t, result, "total_edges")
}

// TestAnalyzeCommand_ResolutionOutcomes ensures the resolution_outcomes kind returns valid JSON.
func TestAnalyzeCommand_ResolutionOutcomes(t *testing.T) {
	dir := t.TempDir()
	writeAnalyzeFixture(t, dir)

	out := runAnalyzeCmd(t, "resolution_outcomes", dir, "on", "json")
	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.Contains(t, result, "by_reason")
	assert.Contains(t, result, "total")
	assert.Contains(t, result, "rows")
}

// TestAnalyzeCommand_UnsupportedKind ensures an unsupported --kind returns an error.
func TestAnalyzeCommand_UnsupportedKind(t *testing.T) {
	dir := t.TempDir()

	analyzeKind = "bogus_kind"
	analyzePath = dir
	analyzeTemporal = "on"
	analyzeFormat = "json"

	var buf bytes.Buffer
	analyzeCmd.SetOut(&buf)
	analyzeCmd.SetErr(&bytes.Buffer{})

	err := analyzeCmd.RunE(analyzeCmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "bogus_kind")
}

// TestAnalyzeCommand_HelpFlags verifies the command has all required flags.
func TestAnalyzeCommand_HelpFlags(t *testing.T) {
	flags := analyzeCmd.Flags()

	require.NotNil(t, flags.Lookup("kind"), "--kind flag must exist")
	require.NotNil(t, flags.Lookup("path"), "--path flag must exist")
	require.NotNil(t, flags.Lookup("temporal"), "--temporal flag must exist")
	require.NotNil(t, flags.Lookup("format"), "--format flag must exist")
}

// Ensure context import is used (avoids unused import errors if test runner
// strips the import when context is referenced only via the fixture strings).
var _ = context.Background
