package exporter

// understand_empty_commit_test.go — regression guard for the GitCommitHash
// struct-tag fix (L1.1). The UA ProjectMetaSchema requires gitCommitHash
// (z.string()): an empty string is valid, but an OMITTED field is a validation
// fatal. A commit-less daemon / multi-repo export therefore MUST still emit
// `"gitCommitHash":""`. Before the fix the field carried `omitempty`, which
// dropped it on an empty commit and failed validateGraph.

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWriteUnderstandAnything_EmptyCommit asserts that with GitCommit="" the
// marshaled UA JSON still CONTAINS the required gitCommitHash field (as an empty
// string), and — via the always-on node harness — that validateGraph reports
// success with zero dropped / zero fatal on the commit-less envelope (AC3).
func TestWriteUnderstandAnything_EmptyCommit(t *testing.T) {
	g := buildFixtureGraph()
	opts := UAOptions{
		Granularity: GranularitySlim,
		ProjectName: "fixt",
		AnalyzedAt:  "2026-01-01T00:00:00Z", // fixed by the (test) Action layer
		GitCommit:   "",                     // commit-less daemon / multi-repo export
	}

	// Pretty so the harness receives the same shape a real --pretty export
	// produces; the field-presence check is independent of indentation.
	opts.Pretty = true
	var buf bytes.Buffer
	stats, err := WriteUnderstandAnything(&buf, g, opts)
	require.NoError(t, err)
	got := buf.Bytes()

	// LDD-spirit trajectory: print accounting BEFORE asserting.
	t.Logf("[IMP:9] empty-commit export nodes=%d edges=%d bytes=%d", stats.NodesWritten, stats.EdgesWritten, stats.BytesWritten)

	// (a) The required field must be PRESENT as an empty string, not omitted.
	var probe struct {
		Project struct {
			GitCommitHash *string `json:"gitCommitHash"`
		} `json:"project"`
	}
	require.NoError(t, json.Unmarshal(got, &probe))
	require.NotNil(t, probe.Project.GitCommitHash, "gitCommitHash must be PRESENT (not omitted) even when the commit is empty")
	assert.Equal(t, "", *probe.Project.GitCommitHash, "empty commit must marshal as an empty string")
	assert.Contains(t, string(got), `"gitCommitHash": ""`, "the empty-string gitCommitHash field must appear in the JSON")

	// (b) AUTHORITATIVE UA validateGraph on the commit-less envelope (AC3).
	// Run as a subtest so a skip (node/UA core absent) does not mask (a).
	t.Run("authoritative_validateGraph", func(t *testing.T) {
		runAuthoritativeValidation(t, got)
	})
}
