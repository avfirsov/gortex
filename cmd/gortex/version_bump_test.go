package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	semver "github.com/zzet/gortex/internal/version"
)

// TestBumpSemver_Rules pins the bump behavior. Each case is a
// (current, kind, pre, want) — if the rules drift, whatever release
// automation depends on this ordering breaks silently, so freeze it.
func TestBumpSemver_Rules(t *testing.T) {
	tests := []struct {
		name    string
		current string
		kind    string
		pre     string
		want    string
	}{
		{"patch", "0.1.0", "patch", "", "v0.1.1"},
		{"minor resets patch", "0.1.5", "minor", "", "v0.2.0"},
		{"major resets minor and patch", "1.4.7", "major", "", "v2.0.0"},
		{"prerelease added", "0.1.0", "minor", "rc.1", "v0.2.0-rc.1"},
		{"prerelease replaces existing", "0.2.0-rc.1", "patch", "rc.2", "v0.2.1-rc.2"},
		{"patch clears existing prerelease", "0.2.0-rc.1", "patch", "", "v0.2.1"},
		{"bump tolerates v-prefixed input", "v0.1.0", "minor", "", "v0.2.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := bumpSemver(tt.current, tt.kind, tt.pre)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got.String())
		})
	}
}

func TestBumpSemver_InvalidInput(t *testing.T) {
	// A malformed main.version should surface as an error, not a panic
	// or a silent zero-value — users need to see what went wrong.
	_, err := bumpSemver("dev", "patch", "")
	assert.Error(t, err)
}

// TestVersionBump_RewritesMainGo is the end-to-end proof: given a
// main.go with a version line, runVersionBump rewrites exactly that
// line and nothing else. Spin up a throwaway repo structure so the
// test doesn't touch the real main.go.
func TestVersionBump_RewritesMainGo(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "cmd", "gortex"), 0o755))

	const sourceBefore = `package main

var (
	version = "0.2.0"     // SemVer string
	commit  = ""
	date    = ""
)

func main() {}
`
	path := filepath.Join(dir, "cmd", "gortex", "main.go")
	require.NoError(t, os.WriteFile(path, []byte(sourceBefore), 0o644))

	// runVersionBump reads cmd/gortex/main.go relative to cwd, so chdir.
	oldCwd, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer func() { _ = os.Chdir(oldCwd) }()

	require.NoError(t, runVersionBump(versionBumpCmd, []string{"minor"}))

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	got := string(after)

	assert.Contains(t, got, `version = "0.3.0"`, "minor bump must rewrite the quoted value")
	assert.Contains(t, got, "// SemVer string", "the trailing comment must be preserved")
	assert.Contains(t, got, `commit  = ""`, "sibling var declarations must be untouched")
	assert.NotContains(t, got, `version = "0.2.0"`, "old version must not linger")
}

func TestVersionBump_PreFlagAttachesIdentifier(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "cmd", "gortex"), 0o755))
	path := filepath.Join(dir, "cmd", "gortex", "main.go")
	require.NoError(t, os.WriteFile(path,
		[]byte("package main\n\nvar version = \"0.2.0\"\n"), 0o644))

	oldCwd, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer func() { _ = os.Chdir(oldCwd) }()

	// Simulate `gortex version bump minor --pre rc.1` by setting the
	// package-level flag variable and invoking the Run handler.
	versionBumpPrerelease = "rc.1"
	defer func() { versionBumpPrerelease = "" }()

	require.NoError(t, runVersionBump(versionBumpCmd, []string{"minor"}))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(got), `version = "0.3.0-rc.1"`,
		"bump --pre must attach the prerelease identifier")
}

func TestVersionBump_RefusesOutsideRepo(t *testing.T) {
	// Running bump outside a repo (no cmd/gortex/main.go) must produce
	// a clear error, not crash.
	dir := t.TempDir()
	oldCwd, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer func() { _ = os.Chdir(oldCwd) }()

	err := runVersionBump(versionBumpCmd, []string{"patch"})
	require.Error(t, err)
	assert.True(t,
		strings.Contains(err.Error(), "repo root") ||
			strings.Contains(err.Error(), "main.go"),
		"error should hint at the missing file or repo root: %v", err)
}

// TestCanonicalVersion_UsesSemverPackage sanity-checks the
// integration between main's version vars and internal/version. Keeps
// a lightweight guard so renaming one side doesn't silently break the
// canonical format agents + daemon consumers rely on.
func TestCanonicalVersion_UsesSemverPackage(t *testing.T) {
	// Override the main package variables through the *FromMain funcs'
	// closures. Easiest: mutate the variables directly for the test.
	origVersion, origCommit := version, commit
	defer func() { version, commit = origVersion, origCommit }()

	version = "1.2.3"
	commit = "abc1234"
	got := canonicalVersion()
	want := semver.Version{Major: 1, Minor: 2, Patch: 3, Build: "abc1234"}.String()
	assert.Equal(t, want, got)
}
