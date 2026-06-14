package config

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func writeTemporalAllowlist(t *testing.T, repoPath, body string) {
	t.Helper()
	dir := filepath.Join(repoPath, ".gortex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "temporal-allowlist.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestLoadLocalTemporalEnvHelpers_GateOff(t *testing.T) {
	dir := t.TempDir()
	writeTemporalAllowlist(t, dir, "env_helpers:\n  - FetchActivityName\n")
	t.Setenv(LocalTemporalOptInEnv, "") // not opted in
	if got := LoadLocalTemporalEnvHelpers(dir); got != nil {
		t.Fatalf("expected nil without opt-in, got %v", got)
	}
}

func TestLoadLocalTemporalEnvHelpers_GateOnReadsFile(t *testing.T) {
	dir := t.TempDir()
	writeTemporalAllowlist(t, dir, "env_helpers:\n  - FetchActivityName\n  - GetActivity\n  - \"\"\n")
	t.Setenv(LocalTemporalOptInEnv, "1")
	got := LoadLocalTemporalEnvHelpers(dir)
	sort.Strings(got)
	want := []string{"FetchActivityName", "GetActivity"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v (blank entries dropped)", got, want)
	}
}

func TestLoadLocalTemporalEnvHelpers_GateOnNoFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(LocalTemporalOptInEnv, "true")
	if got := LoadLocalTemporalEnvHelpers(dir); got != nil {
		t.Fatalf("expected nil when file absent, got %v", got)
	}
}

func TestLoadLocalTemporalJavaInvokers_GateOnReadsFile(t *testing.T) {
	dir := t.TempDir()
	writeTemporalAllowlist(t, dir, "java_temporal_invokers:\n  - Invoker\n  - WorkflowClient\njava_temporal_invoker_methods:\n  - dispatch\n")
	t.Setenv(LocalTemporalOptInEnv, "1")
	inv := LoadLocalTemporalJavaInvokers(dir)
	sort.Strings(inv)
	if len(inv) != 2 || inv[0] != "Invoker" || inv[1] != "WorkflowClient" {
		t.Fatalf("invokers = %v, want [Invoker WorkflowClient]", inv)
	}
	if m := LoadLocalTemporalJavaInvokerMethods(dir); len(m) != 1 || m[0] != "dispatch" {
		t.Fatalf("methods = %v, want [dispatch]", m)
	}
}

func TestLoadLocalTemporalJavaInvokers_GateOff(t *testing.T) {
	dir := t.TempDir()
	writeTemporalAllowlist(t, dir, "java_temporal_invokers:\n  - Invoker\n")
	t.Setenv(LocalTemporalOptInEnv, "")
	if got := LoadLocalTemporalJavaInvokers(dir); got != nil {
		t.Fatalf("expected nil without opt-in, got %v", got)
	}
}

func TestLoadLocalTemporalEnvHelpers_GateOnMalformed(t *testing.T) {
	dir := t.TempDir()
	writeTemporalAllowlist(t, dir, "env_helpers: : not yaml :::\n")
	t.Setenv(LocalTemporalOptInEnv, "1")
	if got := LoadLocalTemporalEnvHelpers(dir); got != nil {
		t.Fatalf("expected nil on malformed file (fail-soft), got %v", got)
	}
}
