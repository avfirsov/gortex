package config

// Repo-local, git-ignored Temporal allow-list.
//
// PURPOSE: let a corporate agent extend the Temporal env-helper allow-list with
// project-specific helper names WITHOUT committing those names into the source
// tree (and so without leaking them upstream). The names live in a git-ignored
// `.gortex/temporal-allowlist.yaml`; matching one promotes a dispatch from the
// generic "env"-name heuristic (hidden, speculative) to the allow-list tier
// (visible, inferred).
//
// RATIONALE: mirrors the repo-local `.gortex/providers.json` opt-in
// (GORTEX_ALLOW_LOCAL_PROVIDERS): a checked-out repo could otherwise inject
// names that change how the indexer attributes dispatch, so the file is read
// ONLY behind an explicit env gate. Fail-soft throughout — a missing gate,
// missing file, or malformed file simply yields no extra names.
//
// KEYWORDS: temporal, allow-list, env-helper, repo-local, opt-in, git-ignored

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LocalTemporalOptInEnv gates loading the repo-local Temporal allow-list.
const LocalTemporalOptInEnv = "GORTEX_ALLOW_LOCAL_TEMPORAL"

// localTemporalAllowlistPath is the repo-local allow-list file, under the
// per-repo `.gortex/` directory (which should be git-ignored).
func localTemporalAllowlistPath(repoPath string) string {
	return filepath.Join(repoPath, ".gortex", "temporal-allowlist.yaml")
}

// temporalAllowlistFile is the on-disk shape. Categories are independent: a
// repo may declare any subset (env_helpers for Go env-default promotion;
// java_temporal_invokers / java_temporal_invoker_methods for the Java invoker
// detector).
type temporalAllowlistFile struct {
	EnvHelpers         []string `yaml:"env_helpers"`
	JavaInvokers       []string `yaml:"java_temporal_invokers"`
	JavaInvokerMethods []string `yaml:"java_temporal_invoker_methods"`
}

// loadTemporalAllowlistFile parses the repo-local allow-list once, gated by the
// opt-in env var. Returns (nil, false) when the gate is off, the file is
// missing, or it is malformed (all fail-soft).
func loadTemporalAllowlistFile(repoPath string) (*temporalAllowlistFile, bool) {
	if !localTemporalOptedIn() {
		return nil, false
	}
	raw, err := os.ReadFile(localTemporalAllowlistPath(repoPath))
	if err != nil {
		return nil, false
	}
	var f temporalAllowlistFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, false
	}
	return &f, true
}

// trimNonEmpty drops blank / whitespace-only entries, returning nil when none
// remain.
func trimNonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, n := range in {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// LoadLocalTemporalEnvHelpers returns the corporate env-helper names declared
// in the repo-local `.gortex/temporal-allowlist.yaml`, or nil. Fail-soft: the
// built-in allow-list and the generic heuristic still apply when absent.
func LoadLocalTemporalEnvHelpers(repoPath string) []string {
	f, ok := loadTemporalAllowlistFile(repoPath)
	if !ok {
		return nil
	}
	return trimNonEmpty(f.EnvHelpers)
}

// LoadLocalTemporalJavaInvokers returns the corporate Java Temporal invoker
// class simple-names, or nil. An empty list keeps invoker detection OFF (zero
// behavioural change), so the Java extractor only acts when this is populated.
func LoadLocalTemporalJavaInvokers(repoPath string) []string {
	f, ok := loadTemporalAllowlistFile(repoPath)
	if !ok {
		return nil
	}
	return trimNonEmpty(f.JavaInvokers)
}

// LoadLocalTemporalJavaInvokerMethods returns the corporate override for the
// invoker method names (default set applies when nil): the methods on an
// invoker type that dispatch a workflow/activity.
func LoadLocalTemporalJavaInvokerMethods(repoPath string) []string {
	f, ok := loadTemporalAllowlistFile(repoPath)
	if !ok {
		return nil
	}
	return trimNonEmpty(f.JavaInvokerMethods)
}

// localTemporalOptedIn reports whether the repo-local allow-list opt-in env var
// is set to a truthy value.
func localTemporalOptedIn() bool {
	v := strings.TrimSpace(os.Getenv(LocalTemporalOptInEnv))
	return v == "1" || strings.EqualFold(v, "true")
}
