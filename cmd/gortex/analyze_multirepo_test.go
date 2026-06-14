package main

// PURPOSE — integration test for `gortex analyze --repo` (multi-repo merged
// store). Demonstrates the measurement-methodology fix: a cross-repo Temporal
// dispatch is a broken_dispatch when each repo is analysed alone, but resolves
// when both are analysed together in one merged store.
// KEYWORDS — analyze, --repo, multi-repo, cross-repo, temporal_orphans

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runAnalyzeRepos invokes analyzeCmd with --repo flags and returns parsed JSON.
func runAnalyzeRepos(t *testing.T, kind string, repos ...string) map[string]any {
	t.Helper()
	prevKind, prevPath, prevRepos := analyzeKind, analyzePath, analyzeRepos
	prevTemporal, prevFormat := analyzeTemporal, analyzeFormat
	t.Cleanup(func() {
		analyzeKind, analyzePath, analyzeRepos = prevKind, prevPath, prevRepos
		analyzeTemporal, analyzeFormat = prevTemporal, prevFormat
	})
	analyzeKind, analyzePath, analyzeRepos = kind, ".", repos
	analyzeTemporal, analyzeFormat = "on", "json"

	var buf bytes.Buffer
	analyzeCmd.SetOut(&buf)
	analyzeCmd.SetErr(&bytes.Buffer{})
	require.NoError(t, analyzeCmd.RunE(analyzeCmd, nil))

	var m map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
	return m
}

// brokenDispatchTotal reads totals.broken_dispatch from the orphan report JSON.
func brokenDispatchTotal(t *testing.T, m map[string]any) int {
	t.Helper()
	totals, ok := m["totals"].(map[string]any)
	require.True(t, ok, "totals must be present")
	c, ok := totals["broken_dispatch"].(float64)
	require.True(t, ok, "totals.broken_dispatch must be a number")
	return int(c)
}

// TestAnalyzeCommand_MultiRepoCrossRepoResolves is the CLI-level proof of the
// methodology fix: repo A dispatches "ChargeActivity" which repo B registers.
// Single-repo analysis can't resolve it (broken_dispatch=1); analysing both
// repos together via --repo merges them into one store and resolves it
// (broken_dispatch=0).
func TestAnalyzeCommand_MultiRepoCrossRepoResolves(t *testing.T) {
	repoA := t.TempDir()
	writeAnalyzeFile(t, filepath.Join(repoA, "workflow.go"), `package wf

import "go.temporal.io/sdk/workflow"

func OrderWorkflow(ctx workflow.Context) error {
	return workflow.ExecuteActivity(ctx, "ChargeActivity", nil).Get(ctx, nil)
}
`)
	repoB := t.TempDir()
	writeAnalyzeFile(t, filepath.Join(repoB, "activity.go"), `package act

import "context"

func ChargeActivity(ctx context.Context) error { return nil }

func setupWorker(w Worker) { w.RegisterActivity(ChargeActivity) }
`)

	// Repo A alone: the dispatch's handler lives elsewhere → 1 broken_dispatch.
	single := runAnalyzeRepos(t, "temporal_orphans", repoA)
	assert.Equal(t, 1, brokenDispatchTotal(t, single),
		"single-repo analysis cannot resolve the cross-repo dispatch")

	// Both repos in one merged store via --repo → the dispatch resolves.
	merged := runAnalyzeRepos(t, "temporal_orphans", repoA, repoB)
	assert.Equal(t, 0, brokenDispatchTotal(t, merged),
		"multi-repo --repo analysis resolves the cross-repo dispatch")
}

// TestAnalyzeCommand_RepoFlagExists guards the new flag.
func TestAnalyzeCommand_RepoFlagExists(t *testing.T) {
	require.NotNil(t, analyzeCmd.Flags().Lookup("repo"), "--repo flag must exist")
}
