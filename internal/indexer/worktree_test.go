package indexer

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// realpath resolves symlinks so macOS's /var → /private/var aliasing
// does not break a path comparison.
func realpath(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	require.NoError(t, err)
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

func TestResolveWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available in PATH")
	}

	main := t.TempDir()
	runGit(t, main, "init", "-q", "-b", "main")
	runGit(t, main, "config", "user.email", "test@example.com")
	runGit(t, main, "config", "user.name", "Test")
	runGit(t, main, "config", "commit.gpgsign", "false")
	writeFile(t, filepath.Join(main, "a.go"), "package main\n")
	runGit(t, main, "add", ".")
	runGit(t, main, "commit", "-q", "-m", "init")

	// The main checkout resolves to itself.
	mainInfo := ResolveWorktree(main)
	require.False(t, mainInfo.IsWorktree, "the main checkout is not a worktree")
	require.Equal(t, realpath(t, main), realpath(t, mainInfo.MainRepoPath))
	require.NotEmpty(t, mainInfo.GitCommonDir)

	// A linked worktree on a new branch.
	wt := filepath.Join(t.TempDir(), "feature-wt")
	runGit(t, main, "worktree", "add", "-q", "-b", "feature", wt)

	wtInfo := ResolveWorktree(wt)
	require.True(t, wtInfo.IsWorktree, "the linked worktree must be detected")
	require.Equal(t, realpath(t, main), realpath(t, wtInfo.MainRepoPath),
		"a worktree must resolve to the main repo it shares .git with")
	require.Equal(t, realpath(t, mainInfo.GitCommonDir), realpath(t, wtInfo.GitCommonDir),
		"the worktree and the main checkout share one .git common dir")
}

func TestResolveWorktree_NonGitDir(t *testing.T) {
	dir := t.TempDir()
	info := ResolveWorktree(dir)
	require.False(t, info.IsWorktree)
	require.Equal(t, realpath(t, dir), realpath(t, info.MainRepoPath))
	require.Empty(t, info.GitCommonDir)
}
