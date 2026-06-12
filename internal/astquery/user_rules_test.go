package astquery

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadUserRulesData_RegistersRules(t *testing.T) {
	src := `
[[rule]]
name = "fr80-feature-flag"
description = "flags.Register calls"
domain = "feature_flag"
severity = "info"
tags = ["flags"]
[rule.query]
go = '(call_expression) @match'

[[rule]]
name = "fr80-cli-command"
domain = "cli_command"
[rule.query]
go = '(function_declaration) @match'
`
	n, err := LoadUserRulesData([]byte(src))
	require.NoError(t, err)
	require.Equal(t, 2, n)

	d := LookupDetector("user:fr80-feature-flag")
	require.NotNil(t, d)
	require.Equal(t, "domain", d.Category)
	require.Contains(t, d.Tags, "feature_flag")
	require.Contains(t, d.Tags, "user-rule")
	require.Contains(t, d.Tags, "flags")
	require.Equal(t, "(call_expression) @match", d.Languages["go"])

	// The domain category bundle now includes the user rules — this
	// is what `analyze kind=domain` fans out over.
	names := map[string]bool{}
	for _, dd := range DetectorsByCategory("domain") {
		names[dd.Name] = true
	}
	require.True(t, names["user:fr80-feature-flag"])
	require.True(t, names["user:fr80-cli-command"])
}

func TestLoadUserRulesData_DefaultsApplied(t *testing.T) {
	n, err := LoadUserRulesData([]byte("[[rule]]\nname = \"fr80-defaults\"\n[rule.query]\ngo = '(x) @match'\n"))
	require.NoError(t, err)
	require.Equal(t, 1, n)
	d := LookupDetector("user:fr80-defaults")
	require.NotNil(t, d)
	require.Equal(t, "info", d.Severity) // default severity
	require.Contains(t, d.Tags, "custom") // default domain
}

func TestLoadUserRulesData_InvalidTOML(t *testing.T) {
	_, err := LoadUserRulesData([]byte("[[rule]\nname = "))
	require.Error(t, err)
}

func TestLoadUserRulesData_MissingName(t *testing.T) {
	_, err := LoadUserRulesData([]byte("[[rule]]\n[rule.query]\ngo = '(x) @match'\n"))
	require.Error(t, err)
}

func TestLoadUserRulesData_MissingQuery(t *testing.T) {
	_, err := LoadUserRulesData([]byte("[[rule]]\nname = \"fr80-noquery\"\n"))
	require.Error(t, err)
}

func TestLoadUserRulesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rules.toml")
	require.NoError(t, os.WriteFile(path,
		[]byte("[[rule]]\nname = \"fr80-fromfile\"\n[rule.query]\ngo = '(x) @match'\n"), 0o644))

	n, err := LoadUserRulesFile(path)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.NotNil(t, LookupDetector("user:fr80-fromfile"))

	_, err = LoadUserRulesFile(filepath.Join(dir, "does-not-exist.toml"))
	require.Error(t, err)
}
