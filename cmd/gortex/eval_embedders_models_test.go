package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestNextGenModelSpecs_AllRegistered(t *testing.T) {
	specs := nextGenModelSpecs()
	want := []string{"embedding-gemma", "qwen3-embedding-8b", "nv-embed-v2", "potion-code-16m"}
	got := map[string]bool{}
	for _, s := range specs {
		got[s.Name] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("registry missing spec %q (have: %v)", w, mapKeys(got))
		}
	}
}

func TestNextGenModelSpecs_CarryInstallHints(t *testing.T) {
	for _, s := range nextGenModelSpecs() {
		if s.InstallHint == "" {
			t.Errorf("spec %q has empty install hint — operators won't know what to install", s.Name)
		}
		if s.LoaderCheck == nil {
			t.Errorf("spec %q has nil LoaderCheck — --list can't probe availability", s.Name)
		}
		if s.Provider == "" {
			t.Errorf("spec %q has empty provider — list grouping breaks", s.Name)
		}
		if s.Dim <= 0 {
			t.Errorf("spec %q has invalid dim %d", s.Name, s.Dim)
		}
	}
}

func TestModelSpecByName_CaseInsensitive(t *testing.T) {
	for _, name := range []string{"potion-code-16m", "POTION-CODE-16M", "Potion-Code-16M"} {
		if got := modelSpecByName(name); got == nil {
			t.Errorf("modelSpecByName(%q) = nil, want a match", name)
		}
	}
}

func TestModelSpecByName_UnknownReturnsNil(t *testing.T) {
	if got := modelSpecByName("does-not-exist"); got != nil {
		t.Errorf("modelSpecByName(unknown) = %v, want nil", got)
	}
}

func TestPythonModulePresent_ImpossibleModuleReturnsError(t *testing.T) {
	check := pythonModulePresent("this_module_should_definitely_not_exist_xyz")
	if err := check(); err == nil {
		t.Error("expected error for impossible module")
	}
}

func TestEvalEmbeddersListCmd_PopulatesAllSections(t *testing.T) {
	var buf bytes.Buffer
	evalEmbeddersListCmd.SetOut(&buf)
	if err := evalEmbeddersListCmd.RunE(evalEmbeddersListCmd, nil); err != nil {
		t.Fatalf("list cmd failed: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"# Embedder model registry",
		"## Next-gen specs",
		"## MiniLM-L6-v2 ONNX variants",
		"embedding-gemma",
		"qwen3-embedding-8b",
		"nv-embed-v2",
		"potion-code-16m",
		"fp32",
		"qint8_arm64",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--list output missing %q\n----\n%s", want, out)
		}
	}
}

func TestEvalEmbeddersListCmd_AvailabilityColumnPopulates(t *testing.T) {
	var buf bytes.Buffer
	evalEmbeddersListCmd.SetOut(&buf)
	_ = evalEmbeddersListCmd.RunE(evalEmbeddersListCmd, nil)
	out := buf.String()
	// Either ✓ or ✗ must appear for each spec — empty cell would
	// mean the LoaderCheck panicked or got skipped.
	checkmarks := strings.Count(out, "✓") + strings.Count(out, "✗")
	if checkmarks < len(nextGenModelSpecs()) {
		t.Errorf("availability column under-populated: %d marks for %d specs", checkmarks, len(nextGenModelSpecs()))
	}
}

func TestEvalEmbeddersListCmd_Wired(t *testing.T) {
	// The list subcommand must be a child of evalEmbeddersCmd (not
	// of evalCmd or somewhere stray). Otherwise `gortex eval
	// embedders list` doesn't route.
	subs := map[string]bool{}
	for _, c := range evalEmbeddersCmd.Commands() {
		subs[c.Name()] = true
	}
	if !subs["list"] {
		t.Errorf("evalEmbeddersCmd missing `list` subcommand; have %v", subs)
	}
}

func mapKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Used only to keep imports tidy when Go complains about a stray
// fmt-related lint while iterating; remove if not needed.
var _ = fmt.Sprintf
