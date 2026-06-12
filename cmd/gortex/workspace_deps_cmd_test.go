package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/config"
)

func TestNormalizeDepMode(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "read-only", false},
		{"read-only", "read-only", false},
		{"  read-only  ", "read-only", false},
		{"read-write", "", true},
		{"rw", "", true},
	}
	for _, tc := range cases {
		got, err := normalizeDepMode(tc.in)
		if tc.wantErr {
			require.Error(t, err, "input %q", tc.in)
			continue
		}
		require.NoError(t, err, "input %q", tc.in)
		assert.Equal(t, tc.want, got)
	}
}

func TestMergeModules(t *testing.T) {
	got := mergeModules(
		[]string{"a", "b", " "},
		[]string{"b", "c", "a", "  d  "},
	)
	assert.Equal(t, []string{"a", "b", "c", "d"}, got)

	assert.Empty(t, mergeModules(nil, []string{"", "  "}))
}

func TestUpsertCrossWorkspaceDep_New(t *testing.T) {
	deps, extended := upsertCrossWorkspaceDep(nil, "gortex", []string{"m1", "m1", "m2"}, "read-only", false)
	require.False(t, extended)
	require.Len(t, deps, 1)
	assert.Equal(t, "gortex", deps[0].Workspace)
	assert.Equal(t, []string{"m1", "m2"}, deps[0].Modules)
	assert.Equal(t, "read-only", deps[0].Mode)
}

func TestUpsertCrossWorkspaceDep_ExtendMergesModules(t *testing.T) {
	start := []config.CrossWorkspaceDep{
		{Workspace: "gortex", Modules: []string{"m1"}, Mode: "read-only"},
	}
	deps, extended := upsertCrossWorkspaceDep(start, "gortex", []string{"m1", "m2"}, "read-only", false)
	require.True(t, extended)
	require.Len(t, deps, 1)
	assert.Equal(t, []string{"m1", "m2"}, deps[0].Modules)
}

func TestUpsertCrossWorkspaceDep_ModeOnlyChangesWhenExplicit(t *testing.T) {
	start := []config.CrossWorkspaceDep{
		{Workspace: "gortex", Modules: []string{"m1"}, Mode: "read-only"},
	}
	// Not explicit, existing mode set → unchanged.
	deps, _ := upsertCrossWorkspaceDep(start, "gortex", []string{"m2"}, "read-only", false)
	assert.Equal(t, "read-only", deps[0].Mode)

	// Empty existing mode is filled even when not explicit.
	start2 := []config.CrossWorkspaceDep{{Workspace: "gortex", Modules: []string{"m1"}}}
	deps2, _ := upsertCrossWorkspaceDep(start2, "gortex", []string{"m2"}, "read-only", false)
	assert.Equal(t, "read-only", deps2[0].Mode)
}

func TestSetCrossWorkspaceDepMode(t *testing.T) {
	start := []config.CrossWorkspaceDep{
		{Workspace: "gortex", Modules: []string{"m1"}, Mode: "read-only"},
	}
	deps, err := setCrossWorkspaceDepMode(start, "gortex", "read-only")
	require.NoError(t, err)
	assert.Equal(t, "read-only", deps[0].Mode)

	_, err = setCrossWorkspaceDepMode(start, "nope", "read-only")
	require.Error(t, err)
}

func TestRemoveCrossWorkspaceDep_WholeEntry(t *testing.T) {
	start := []config.CrossWorkspaceDep{
		{Workspace: "gortex", Modules: []string{"m1"}},
		{Workspace: "other", Modules: []string{"m2"}},
	}
	deps, err := removeCrossWorkspaceDep(start, "gortex", nil)
	require.NoError(t, err)
	require.Len(t, deps, 1)
	assert.Equal(t, "other", deps[0].Workspace)
}

func TestRemoveCrossWorkspaceDep_Modules(t *testing.T) {
	start := []config.CrossWorkspaceDep{
		{Workspace: "gortex", Modules: []string{"m1", "m2", "m3"}},
	}
	deps, err := removeCrossWorkspaceDep(start, "gortex", []string{"m2"})
	require.NoError(t, err)
	require.Len(t, deps, 1)
	assert.Equal(t, []string{"m1", "m3"}, deps[0].Modules)

	// Removing the last module drops the entry entirely.
	deps, err = removeCrossWorkspaceDep(deps, "gortex", []string{"m1", "m3"})
	require.NoError(t, err)
	assert.Empty(t, deps)
}

func TestRemoveCrossWorkspaceDep_Errors(t *testing.T) {
	start := []config.CrossWorkspaceDep{
		{Workspace: "gortex", Modules: []string{"m1"}},
	}
	_, err := removeCrossWorkspaceDep(start, "missing", nil)
	require.Error(t, err)

	_, err = removeCrossWorkspaceDep(start, "gortex", []string{"not-there"})
	require.Error(t, err)
}

func TestReadWriteCrossWorkspaceDeps_RoundTrip(t *testing.T) {
	root := t.TempDir()
	// A pre-existing key that must survive the edit untouched.
	require.NoError(t, os.WriteFile(
		filepath.Join(root, ".gortex.yaml"),
		[]byte("workspace: tuck\nexclude:\n  - vendor/\n"),
		0o644))

	deps := []config.CrossWorkspaceDep{
		{Workspace: "gortex", Modules: []string{"github.com/gortexhq/gortex"}, Mode: "read-only"},
	}
	require.NoError(t, writeCrossWorkspaceDeps(root, deps))

	payload, err := readRepoYAML(root)
	require.NoError(t, err)
	assert.Equal(t, "tuck", payload["workspace"])
	assert.NotNil(t, payload["exclude"])

	got, err := readCrossWorkspaceDeps(payload)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, deps, got)

	// The written file must load + validate cleanly through the real loader.
	cfg, err := config.Load(filepath.Join(root, ".gortex.yaml"))
	require.NoError(t, err)
	require.Len(t, cfg.CrossWorkspaceDeps, 1)
	assert.Equal(t, "gortex", cfg.CrossWorkspaceDeps[0].Workspace)

	// Empty list deletes the key rather than leaving `cross_workspace_deps: []`.
	require.NoError(t, writeCrossWorkspaceDeps(root, nil))
	payload, err = readRepoYAML(root)
	require.NoError(t, err)
	_, present := payload["cross_workspace_deps"]
	assert.False(t, present)
	assert.Equal(t, "tuck", payload["workspace"])
}

// depsTestEnv wires a temp global config pointing at a single tracked
// repo, sets the package-level cfgFile so loadGlobalRepos picks it up,
// and returns the repo path. cfgFile is restored on cleanup.
func depsTestEnv(t *testing.T) (repoPath string) {
	t.Helper()
	root := t.TempDir()
	repoPath = filepath.Join(root, "tuck-api")
	require.NoError(t, os.MkdirAll(filepath.Join(repoPath, ".git"), 0o755))

	gc := &config.GlobalConfig{
		Repos: []config.RepoEntry{{Path: repoPath, Name: "tuck-api", Workspace: "tuck"}},
	}
	gcPath := filepath.Join(root, "config.yaml")
	gc.SetConfigPath(gcPath)
	require.NoError(t, gc.Save())

	prev := cfgFile
	cfgFile = gcPath
	t.Cleanup(func() { cfgFile = prev })
	return repoPath
}

func newDepsCmd() (*cobra.Command, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	c := &cobra.Command{}
	c.SetOut(buf)
	c.SetErr(buf)
	return c, buf
}

func TestRunWorkspaceDepsAdd_And_List(t *testing.T) {
	repoPath := depsTestEnv(t)

	cmd, buf := newDepsCmd()
	err := runWorkspaceDepsAdd(cmd, []string{"tuck-api", "gortex", "github.com/gortexhq/gortex"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "declared cross-workspace dependency")
	assert.Contains(t, buf.String(), "tuck → gortex")

	// Re-add with a second module → extends the same entry.
	cmd, buf = newDepsCmd()
	err = runWorkspaceDepsAdd(cmd, []string{"tuck-api", "gortex", "github.com/gortexhq/gcx-go"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "extended cross-workspace dependency")

	cfg, err := config.Load(filepath.Join(repoPath, ".gortex.yaml"))
	require.NoError(t, err)
	require.Len(t, cfg.CrossWorkspaceDeps, 1)
	assert.Equal(t, []string{"github.com/gortexhq/gortex", "github.com/gortexhq/gcx-go"},
		cfg.CrossWorkspaceDeps[0].Modules)

	cmd, buf = newDepsCmd()
	require.NoError(t, runWorkspaceDepsList(cmd, []string{"tuck-api"}))
	out := buf.String()
	assert.Contains(t, out, "gortex")
	assert.Contains(t, out, "github.com/gortexhq/gcx-go")
}

func TestRunWorkspaceDepsAdd_RejectsSelfWorkspace(t *testing.T) {
	depsTestEnv(t)
	cmd, _ := newDepsCmd()
	err := runWorkspaceDepsAdd(cmd, []string{"tuck-api", "tuck", "some/module"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no-op")
}

func TestRunWorkspaceDepsAdd_RejectsBadMode(t *testing.T) {
	depsTestEnv(t)
	prev := workspaceDepsAddMode
	workspaceDepsAddMode = "read-write"
	t.Cleanup(func() { workspaceDepsAddMode = prev })

	cmd, _ := newDepsCmd()
	err := runWorkspaceDepsAdd(cmd, []string{"tuck-api", "gortex", "some/module"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read-only")
}

func TestRunWorkspaceDepsMode(t *testing.T) {
	repoPath := depsTestEnv(t)

	cmd, _ := newDepsCmd()
	require.NoError(t, runWorkspaceDepsAdd(cmd, []string{"tuck-api", "gortex", "some/module"}))

	// Setting read-only on an existing entry succeeds.
	cmd, buf := newDepsCmd()
	require.NoError(t, runWorkspaceDepsMode(cmd, []string{"tuck-api", "gortex", "read-only"}))
	assert.Contains(t, buf.String(), "mode=read-only")

	// read-write is rejected.
	cmd, _ = newDepsCmd()
	err := runWorkspaceDepsMode(cmd, []string{"tuck-api", "gortex", "read-write"})
	require.Error(t, err)

	// Unknown target workspace is rejected.
	cmd, _ = newDepsCmd()
	err = runWorkspaceDepsMode(cmd, []string{"tuck-api", "nope", "read-only"})
	require.Error(t, err)

	cfg, err := config.Load(filepath.Join(repoPath, ".gortex.yaml"))
	require.NoError(t, err)
	require.Len(t, cfg.CrossWorkspaceDeps, 1)
	assert.Equal(t, "read-only", cfg.CrossWorkspaceDeps[0].Mode)
}

func TestRunWorkspaceDepsRemove(t *testing.T) {
	repoPath := depsTestEnv(t)

	cmd, _ := newDepsCmd()
	require.NoError(t, runWorkspaceDepsAdd(cmd, []string{"tuck-api", "gortex", "m1", "m2"}))

	// Remove one module → entry survives.
	cmd, buf := newDepsCmd()
	require.NoError(t, runWorkspaceDepsRemove(cmd, []string{"tuck-api", "gortex", "m1"}))
	assert.Contains(t, buf.String(), "removed module(s)")

	cfg, err := config.Load(filepath.Join(repoPath, ".gortex.yaml"))
	require.NoError(t, err)
	require.Len(t, cfg.CrossWorkspaceDeps, 1)
	assert.Equal(t, []string{"m2"}, cfg.CrossWorkspaceDeps[0].Modules)

	// Remove whole entry.
	cmd, buf = newDepsCmd()
	require.NoError(t, runWorkspaceDepsRemove(cmd, []string{"tuck-api", "gortex"}))
	assert.Contains(t, buf.String(), "removed cross-workspace dependency")

	cfg, err = config.Load(filepath.Join(repoPath, ".gortex.yaml"))
	require.NoError(t, err)
	assert.Empty(t, cfg.CrossWorkspaceDeps)

	// Removing a non-existent entry errors.
	cmd, _ = newDepsCmd()
	err = runWorkspaceDepsRemove(cmd, []string{"tuck-api", "gortex"})
	require.Error(t, err)
}

func TestRunWorkspaceDepsList_Empty(t *testing.T) {
	depsTestEnv(t)
	cmd, buf := newDepsCmd()
	require.NoError(t, runWorkspaceDepsList(cmd, nil))
	assert.Contains(t, buf.String(), "no tracked repo declares cross-workspace dependencies")
}
