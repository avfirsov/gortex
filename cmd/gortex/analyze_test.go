package main

// PURPOSE — integration tests for the daemonless `gortex analyze` command:
// validates kind validation and an end-to-end index+analyze cycle producing
// machine-readable JSON.
// KEYWORDS — analyze, CLI, daemonless, integration

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

func TestRunAnalyze_UnsupportedKind(t *testing.T) {
	analyzeKind = "bogus"
	analyzePath = "."
	analyzeFormat = "json"
	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	if err := runAnalyze(cmd, nil); err == nil {
		t.Fatal("expected error for unsupported --kind")
	}
}

func TestRunAnalyze_SynthesizersE2E(t *testing.T) {
	dir := t.TempDir()
	src := "package p\n\nfunc A() { B() }\n\nfunc B() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	analyzeKind = "synthesizers"
	analyzePath = dir
	analyzeFormat = "json"

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := runAnalyze(cmd, nil); err != nil {
		t.Fatalf("runAnalyze: %v", err)
	}

	var res map[string]any
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, out.String())
	}
	if _, ok := res["synthesizers"]; !ok {
		t.Errorf("expected \"synthesizers\" key in output, got: %s", out.String())
	}
	if _, ok := res["total_edges"]; !ok {
		t.Errorf("expected \"total_edges\" key in output, got: %s", out.String())
	}
}

func TestRunAnalyze_ResolutionOutcomesE2E(t *testing.T) {
	dir := t.TempDir()
	// A call to an undefined function leaves an unresolved edge the
	// resolution_outcomes analyzer can classify.
	src := "package p\n\nfunc A() { missingFunc() }\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	analyzeKind = "resolution_outcomes"
	analyzePath = dir
	analyzeFormat = "json"

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := runAnalyze(cmd, nil); err != nil {
		t.Fatalf("runAnalyze: %v", err)
	}

	var res map[string]any
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, out.String())
	}
	if _, ok := res["by_reason"]; !ok {
		t.Errorf("expected \"by_reason\" key in output, got: %s", out.String())
	}
}
